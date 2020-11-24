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
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/serialize"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
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

func TestNewClient_Stress(t *testing.T) {
	table := []stressTestConfig {
		{
			name:          "RaceConditions",
			expireTimeout: 2 * time.Millisecond,
			minDuration:   0,
			maxDuration:   time.Hour,
			tickDuration:  82 * time.Millisecond / 100,
			iterations:    30000,
		},
		{
			name: "Durations",
			expireTimeout: 100 * time.Millisecond,
			minDuration:   100 * time.Millisecond / 5,
			maxDuration:   100 * time.Millisecond,
			tickDuration:  82 * time.Millisecond,
			iterations:    15,
		},
	}
	for _, q := range table {
		t.Run(q.name, func(t *testing.T) { runStressTest(t, &q) })
	}
}

type stressTestConfig struct {
	name string
	expireTimeout time.Duration
	minDuration, maxDuration time.Duration
	tickDuration time.Duration
	iterations int
}

func runStressTest(t *testing.T, conf *stressTestConfig) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	randSrc := rand.New(rand.NewSource(0))

	refreshTester := newRefreshTesterServer(t, conf.minDuration, conf.maxDuration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(
		serialize.NewClient(),
		updatepath.NewClient("foo"),
		refresh.NewClient3(ctx),
		updatetoken.NewClient(sandbox.GenerateExpiringToken(conf.expireTimeout)),
		adapters.NewServerToClient(refreshTester),
		)

	var oldConn *networkservice.Connection
	for i := 0; i < conf.iterations && !t.Failed(); i++ {
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
			time.Sleep(conf.tickDuration)
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
	time.Sleep(conf.tickDuration)
}

