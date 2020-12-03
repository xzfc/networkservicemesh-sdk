// Copyright (c) 2020 Doc.ai and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package refresh periodically resends NetworkServiceMesh.Request for an
// existing connection so that the Endpoint doesn't 'expire' the networkservice.
package refresh

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/serialize"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

type refreshClient3 struct {
	ctx    context.Context
	timers sync.Map
}

// NewClient3 - creates new NetworkServiceClient chain element for refreshing
// connections before they timeout at the endpoint.
func NewClient3(ctx context.Context) networkservice.NetworkServiceClient {
	return &refreshClient3{
		ctx:    ctx,
		timers: sync.Map{},
	}
}

func (t *refreshClient3) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	connectionID := request.Connection.Id
	t.stopTimer(connectionID)

	t0 := time.Now()
	rv, err := next.Client(ctx).Request(ctx, request.Clone(), opts...)
	path := rv.GetPath()
	if path == nil {
		fmt.Printf("Refresh[%v]: done first, got err delta=%v\n", time.Now(), time.Now().Sub(t0))
	} else {
		fmt.Printf("Refresh[%v]: done first for name=%v id=%v delta=%v\n", time.Now(), path.PathSegments[path.Index].Name, path.PathSegments[path.Index].Id, time.Now().Sub(t0))
	}

	executor := serialize.GetExecutor(ctx)
	if executor == nil {
		return nil, errors.New("no executor provided")
	}
	request.Connection = rv.Clone()
	nextClient := next.Client(ctx)
	t.startTimer(connectionID, executor, nextClient, request, opts)

	return rv, err
}

func (t *refreshClient3) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (e *empty.Empty, err error) {
	t.stopTimer(conn.Id)
	return next.Client(ctx).Close(ctx, conn, opts...)
}

func (t *refreshClient3) stopTimer(connectionID string) {
	value, loaded := t.timers.LoadAndDelete(connectionID)
	if loaded {
		value.(*time.Timer).Stop()
	}
}

func (t *refreshClient3) startTimer(connectionID string, exec serialize.Executor, nextClient networkservice.NetworkServiceClient, request *networkservice.NetworkServiceRequest, opts []grpc.CallOption) {
	path := request.GetConnection().GetPath()
	if path == nil || path.PathSegments == nil || len(path.PathSegments) == 0 ||
		path.Index >= uint32(len(path.PathSegments)) {
		return
	}
	expireTime, err := ptypes.Timestamp(path.PathSegments[path.Index].GetExpires())
	if err != nil {
		return
	}

	// A heuristic to reduce the number of redundant requests in a chain
	// made of refreshing clients with the same expiration time: let outer
	// chain elements refresh slightly faster than inner ones.
	// Update interval is within 0.2*expirationTime .. 0.4*expirationTime
	scale := 1. / 3.
	if len(path.PathSegments) > 1 {
		scale = 0.2 + 0.2*float64(path.Index)/float64(len(path.PathSegments))
	}
	duration := time.Duration(float64(time.Until(expireTime)) * scale)
	fmt.Printf("Refresh[%v]: init name=%v id=%v %v/%v scale=%v in=%v timeouts-in=%v at=%v\n",
		time.Now(), path.PathSegments[path.Index].Name, path.PathSegments[path.Index].Id, path.Index, len(path.PathSegments), scale, duration, time.Until(expireTime), time.Now().Add(duration))

	var timer *time.Timer
	timerStart := time.Now()
	timer = time.AfterFunc(duration, func() {
		fmt.Printf("Refresh[%v]: afterfunc for name=%v id=%v duration(exp/act)=%v/%v\n", time.Now(), path.PathSegments[path.Index].Name, path.PathSegments[path.Index].Id, duration, time.Now().Sub(timerStart))
		exec.AsyncExec(func() {
			oldTimer, _ := t.timers.LoadAndDelete(connectionID)
			if oldTimer == nil {
				return
			}
			if oldTimer.(*time.Timer) != timer {
				oldTimer.(*time.Timer).Stop()
				return
			}

			if t.ctx.Err() != nil {
				// Context is canceled or deadlined.
				return
			}

			fmt.Printf("Refresh[%v]: running repeating for name=%v id=%v\n", time.Now(), path.PathSegments[path.Index].Name, path.PathSegments[path.Index].Id)

			t0 := time.Now()
			rv, err := nextClient.Request(t.ctx, request.Clone(), opts...)
			fmt.Printf("Refresh[%v]: done repeating for name=%v id=%v delta=%v\n", time.Now(), path.PathSegments[path.Index].Name, path.PathSegments[path.Index].Id, time.Now().Sub(t0))

			if err == nil && rv != nil {
				request.Connection = rv
			}

			t.startTimer(connectionID, exec, nextClient, request, opts)
		})
	})
	t.timers.Store(connectionID, timer)
}
