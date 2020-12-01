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
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
)

const (
	// expireTimeout        = 100 * time.Millisecond
	waitForTimeout = expireTimeout
	// tickTimeout          = 10 * time.Millisecond
	refreshCount         = 5
	expectAbsenceTimeout = 5 * expireTimeout

	requestNumber contextKeyType = "RequestNumber"
)

type contextKeyType string

func withRequestNumber(parent context.Context, number int) context.Context {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	return context.WithValue(parent, requestNumber, number)
}

func getRequestNumber(ctx context.Context) int {
	if rv, ok := ctx.Value(requestNumber).(int); ok {
		return rv
	}
	return -1
}

type testRefresh struct {
	RequestFunc func(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error)
}

func (t *testRefresh) Request(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	return t.RequestFunc(ctx, in, opts...)
}

func (t *testRefresh) Close(context.Context, *networkservice.Connection, ...grpc.CallOption) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func hasValue(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func firstGetsValueEarlier(c1, c2 <-chan struct{}) bool {
	select {
	case <-c2:
		return false
	case <-c1:
		return true
	}
}

func TestNewClient_StopRefreshAtAnotherRequest(t *testing.T) {
	t.Skip("https://github.com/networkservicemesh/sdk/issues/260")
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	requestCh := make(chan struct{}, 1)
	testRefresh := &testRefresh{
		RequestFunc: func(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (connection *networkservice.Connection, err error) {
			setExpires(in.GetConnection(), expireTimeout)
			if getRequestNumber(ctx) == 1 {
				requestCh <- struct{}{}
			}
			return in.GetConnection(), nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	request1 := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "conn-1",
		},
	}
	_, err := client.Request(withRequestNumber(context.Background(), 1), request1)
	assert.Nil(t, err)

	assert.True(t, hasValue(requestCh)) // receive value from initial request
	for i := 0; i < refreshCount; i++ {
		require.Eventually(t, func() bool { return hasValue(requestCh) }, waitForTimeout, tickTimeout)
	}

	request2 := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "conn-1",
		},
	}
	conn, err := client.Request(withRequestNumber(context.Background(), 2), request2)
	assert.Nil(t, err)

	absence := make(chan struct{})
	time.AfterFunc(expectAbsenceTimeout, func() {
		absence <- struct{}{}
	})
	assert.True(t, firstGetsValueEarlier(absence, requestCh))

	_, err = client.Close(context.Background(), conn)
	assert.Nil(t, err)
}

const (
	stressExpireTimeout = 25 * time.Millisecond
	stressMinDuration   = stressExpireTimeout / 5
	stressMaxDuration   = stressExpireTimeout * 3 / 2
	stressTick          = 82 * time.Millisecond
)

// TestNewClient_Stress is a stress-test to reveal race-conditions when a request and a
//
// Time graph:
//      time ->
//   C: 0              1              2              ... 99
//   R:  0  0  0  0  0  1  1  1  1  1  2  2  2  2  2 ...  99 99 99 99 99
//
// Legend:
//   C - client requests
//   R - requests sent by refreshClient
//
// Description:
//   ...
func TestNewClient_Stress(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	randSrc := rand.New(rand.NewSource(0))

	refreshTester := newRefreshTesterServer(t, stressMinDuration, stressMaxDuration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(
		updatepath.NewClient("foo"),
		refresh.NewClient(ctx),
		updatetoken.NewClient(sandbox.GenerateExpiringToken(stressExpireTimeout)),
		adapters.NewServerToClient(refreshTester))

	var oldConn *networkservice.Connection
	for i := 0; i < 1000 && !t.Failed(); i++ {
		if i%100 == 0 {
			// TODO: use t.Logf?
			fmt.Println()
		}
		if i%10 == 0 {
			fmt.Printf("%v,", i)
		}

		refreshTester.beforeRequest(strconv.Itoa(i))
		conn, err := client.Request(ctx, mkRequest(0, i, oldConn))
		refreshTester.afterRequest()
		assert.NotNil(t, conn)
		assert.Nil(t, err)
		oldConn = conn

		if t.Failed() {
			break
		}

		if randSrc.Int31n(10) != 0 {
			time.Sleep(stressTick)
		}

		if randSrc.Int31n(10) == 0 {
			refreshTester.beforeClose()
			_, err = client.Close(ctx, oldConn)
			assert.Nil(t, err)
			refreshTester.afterClose()
			oldConn = nil
		}
	}
	fmt.Println()

	if oldConn != nil {
		refreshTester.beforeClose()
		_, _ = client.Close(ctx, oldConn)
		refreshTester.afterClose()
	}
	time.Sleep(stressTick)
}

// TODO: drop this test
func TestNewClient_Reuse(t *testing.T) {
	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			setExpires(r.in.GetConnection(), 10000*time.Millisecond)
			time.Sleep(500 * time.Millisecond)
			fmt.Println("!!!")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	request := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{Id: "conn"},
	}

	var conn1, conn2 *networkservice.Connection
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		conn1, err1 = client.Request(
			withRequestNumber(context.Background(), 1),
			request.Clone())
		wg.Done()
	}()
	time.Sleep(25 * time.Millisecond)
	go func() {
		conn2, err2 = client.Request(
			withRequestNumber(context.Background(), 1),
			request.Clone())
		wg.Done()
	}()
	wg.Wait()

	assert.Nil(t, err1)
	assert.Nil(t, err2)
	fmt.Println(conn1, conn2)
	// TODO: check that conn1 and conn2 are the same
}
