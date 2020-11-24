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

package refresh_test

import (
	"context"
	"fmt"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/cls"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

const (
	endpointName     = "endpoint-name"
	connectionMarker = "refresh-marker"
)

type countClient struct {
	t     *testing.T
	count int32
}

func (c *countClient) validator(atLeast int32) func() bool {
	return func() bool {
		if count := atomic.LoadInt32(&c.count); count < atLeast {
			logrus.Warnf("count %v < atLeast %v", count, atLeast)
			return false
		}
		return true
	}
}

func (c *countClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	request = request.Clone()
	conn := request.GetConnection()

	// Check that refresh updates the request.Connection field (issue #530).
	if atomic.AddInt32(&c.count, 1) == 1 {
		conn.NetworkServiceEndpointName = endpointName
	} else {
		require.Equal(c.t, endpointName, conn.NetworkServiceEndpointName)
	}

	return next.Client(ctx).Request(ctx, request, opts...)
}

func (c *countClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	return next.Client(ctx).Close(ctx, conn, opts...)
}

// refreshTestServer is a helper endpoint to check that the Request()/Close()
// order isn't mixed by refresh (which may happen due to race condition between
// a requests initiated by a client, and a refresh timer), and timer initiated
// request aren't too fast or too slow.
//
// Usage details:
// * Each client Request() should be wrapped in beforeRequest()/afterRequest()
//   calls. Same for Close() and beforeClose()/afterClose().
// * Caveat: parallel client initiated requests aren't supported by this tester.
// * To distinguish between different requests, the value of
//   `Connection.Context.ExtraContext[connectionMarker]` is used as a marker.
type refreshTesterServer struct {
	t           *testing.T
	minDuration time.Duration
	maxDuration time.Duration

	mutex         sync.Mutex
	state         int
	lastSeen      time.Time
	currentMarker string
	nextMarker    string
}

type refreshTesterServerState = int

const (
	testRefreshStateInit        = iota
	testRefreshStateWaitRequest
	testRefreshStateDoneRequest
	testRefreshStateRunning
	testRefreshStateWaitClose
)

func newRefreshTesterServer(t *testing.T, minDuration, maxDuration time.Duration) *refreshTesterServer {
	return &refreshTesterServer{
		t:           t,
		minDuration: minDuration,
		maxDuration: maxDuration,
		state:       testRefreshStateInit,
	}
}

func (t *refreshTesterServer) beforeRequest(marker string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Contains(t.t, []refreshTesterServerState{testRefreshStateInit, testRefreshStateRunning}, t.state, "Unexpected state")
	t.state = testRefreshStateWaitRequest
	t.nextMarker = marker
}

func (t *refreshTesterServer) afterRequest() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateDoneRequest, t.state, "Unexpected state")
	t.state = testRefreshStateRunning
}

func (t *refreshTesterServer) beforeClose() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateRunning, t.state, "Unexpected state")
	t.state = testRefreshStateWaitClose
}

func (t *refreshTesterServer) afterClose() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateWaitClose, t.state, "Unexpected state")
	t.state = testRefreshStateInit
	t.currentMarker = ""
}

func (t *refreshTesterServer) checkUnlocked() {
	if t.state == testRefreshStateDoneRequest || t.state == testRefreshStateRunning {
		delta := time.Now().UTC().Sub(t.lastSeen)
		require.Lessf(t.t, int64(delta), int64(t.maxDuration), "Duration expired (too slow) delta=%v max=%v", delta, t.maxDuration)
	}
}

func (t *refreshTesterServer) Request(_ context.Context, request *networkservice.NetworkServiceRequest) (*networkservice.Connection, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()

	marker := request.Connection.Context.ExtraContext[connectionMarker]
	require.NotEmpty(t.t, marker, "Marker is empty")

	delta := time.Now().UTC().Sub(t.lastSeen)
	fmt.Printf("delta=%v min=%v marker=%v\n", delta, t.minDuration, marker)

	switch t.state {
	case testRefreshStateWaitRequest:
		require.Contains(t.t, []string{t.nextMarker, t.currentMarker}, marker, "Unexpected marker")
		if marker == t.nextMarker {
			t.state = testRefreshStateDoneRequest
			t.currentMarker = t.nextMarker
		}
	case testRefreshStateDoneRequest, testRefreshStateRunning, testRefreshStateWaitClose:
		require.Equal(t.t, t.currentMarker, marker, "Unexpected marker")
		// delta := time.Now().UTC().Sub(t.lastSeen)
		// require.Greaterf(t.t, int64(delta), int64(t.minDuration), "Too fast delta=%v min=%v", delta, t.minDuration)
	default:
		require.Fail(t.t, "Unexpected state", t.state)
	}

	t.lastSeen = time.Now()
	return request.Connection, nil
}

func (t *refreshTesterServer) Close(ctx context.Context, connection *networkservice.Connection) (*empty.Empty, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()

	// TODO: check for closes

	return &empty.Empty{}, nil
}

func mkRequest(marker int, conn *networkservice.Connection) *networkservice.NetworkServiceRequest {
	if conn == nil {
		conn = &networkservice.Connection{
			Id: "conn-id",
			Context: &networkservice.ConnectionContext{
				ExtraContext: map[string]string{
					connectionMarker: strconv.Itoa(marker),
				},
			},
			NetworkService: "my-service-remote",
		}
	} else {
		conn.Context.ExtraContext[connectionMarker] = strconv.Itoa(marker)
	}
	return &networkservice.NetworkServiceRequest{
		MechanismPreferences: []*networkservice.Mechanism{
			{Cls: cls.LOCAL, Type: kernel.MECHANISM},
		},
		Connection: conn,
	}
}
