// Copyright (c) 2020 Cisco Systems, Inc.
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
	"sync"
	"time"

	"github.com/edwarnicke/serialize"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

type refreshClient2 struct {
	ctx   context.Context
	items sync.Map
}

type refreshExecutor struct {
	executor serialize.Executor
	timer    *time.Timer
	refs     int
}

func NewClient(ctx context.Context) networkservice.NetworkServiceClient {
	return NewClient1(ctx)
}

func NewClient2(ctx context.Context) *refreshClient2 {
	return &refreshClient2{
		ctx:   ctx,
		items: sync.Map{},
	}
}

func (q *refreshClient2) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (rv *networkservice.Connection, err error) {
	connectionID := request.Connection.Id
	q.execute(connectionID, func(exec *refreshExecutor) {
		exec.stopTimer()

		rv, err = next.Client(ctx).Request(ctx, request.Clone(), opts...)

		if err != nil || rv == nil {
			return
		}

		request.Connection = rv.Clone()
		nextClient := next.Client(ctx)

		q.startTimer(connectionID, exec, nextClient, request, opts)
	})
	return rv, err
}

func (q *refreshClient2) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (e *empty.Empty, err error) {
	q.execute(conn.Id, func(exec *refreshExecutor) {
		exec.stopTimer()
		e, err = next.Client(ctx).Close(ctx, conn, opts...)
	})
	return e, err
}

func (exec *refreshExecutor) stopTimer() {
	if exec.timer != nil && exec.timer.Stop() {
		exec.refs--
	}
	exec.timer = nil
}

func (q *refreshClient2) startTimer(connectionID string, exec *refreshExecutor, nextClient networkservice.NetworkServiceClient, request *networkservice.NetworkServiceRequest, opts []grpc.CallOption) {
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

	exec.refs++
	var timer *time.Timer
	timer = time.AfterFunc(duration, func() {
		q.execute(connectionID, func(exec *refreshExecutor) {
			if timer != exec.timer {
				// Got an already canceled timer.
				return
			}
			defer func() { exec.refs-- }()
			exec.timer = nil

			if q.ctx.Err() != nil {
				// Context is canceled or deadlined.
				return
			}

			rv, err := nextClient.Request(q.ctx, request.Clone(), opts...)

			if err == nil && rv != nil {
				request.Connection = rv
			}

			q.startTimer(connectionID, exec, nextClient, request, opts)
		})
	})
	exec.timer = timer
}

func (q *refreshClient2) execute(connectionID string, f func(*refreshExecutor)) {
	execInt, loaded := q.items.LoadOrStore(connectionID, &refreshExecutor{refs: 1})
	exec := execInt.(*refreshExecutor)
	<-exec.executor.AsyncExec(func() {
		if loaded {
			exec.refs++
		}

		f(exec)

		exec.refs--
		if exec.refs == 0 {
			q.items.Delete(connectionID)
		}
	})
}
