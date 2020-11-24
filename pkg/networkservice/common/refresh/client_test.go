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
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/serialize"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
)

const (
	expireTimeout     = 100 * time.Millisecond
	eventuallyTimeout = expireTimeout
	tickTimeout       = 10 * time.Millisecond
	neverTimeout      = 5 * expireTimeout
	endpointName      = "endpoint-name"
)

func TestRefreshClient_StopRefreshAtClose(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cloneClient := &countClient{
		t: t,
	}
	client := chain.NewNetworkServiceClient(
		serialize.NewClient(),
		refresh.NewClient(ctx),
		updatepath.NewClient("refresh"),
		updatetoken.NewClient(sandbox.GenerateExpiringToken(expireTimeout)),
		cloneClient,
	)

	conn, err := client.Request(ctx, &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "id",
		},
	})
	require.NoError(t, err)
	require.Condition(t, cloneClient.validator(1))

	require.Eventually(t, cloneClient.validator(2), eventuallyTimeout, tickTimeout)

	_, err = client.Close(ctx, conn)
	require.NoError(t, err)

	count := atomic.LoadInt32(&cloneClient.count)
	require.Never(t, cloneClient.validator(count+1), neverTimeout, tickTimeout)
}

func TestRefreshClient_StopRefreshAtAnotherRequest(t *testing.T) {
	t.Skip("https://github.com/networkservicemesh/sdk/issues/260")
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cloneClient := &countClient{
		t: t,
	}
	client := chain.NewNetworkServiceClient(
		serialize.NewClient(),
		refresh.NewClient(ctx),
		cloneClient,
	)

	conn, err := client.Request(ctx, &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "id",
		},
	})
	require.NoError(t, err)
	require.Condition(t, cloneClient.validator(1))

	require.Eventually(t, cloneClient.validator(2), eventuallyTimeout, tickTimeout)

	_, err = client.Request(ctx, &networkservice.NetworkServiceRequest{
		Connection: conn,
	})
	require.NoError(t, err)

	require.Never(t, cloneClient.validator(3), neverTimeout, tickTimeout)
}

// TestRefreshClient_Serial checks that requests/closes with a same ID are
// performed sequentially.
func TestRefreshClient_Serial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mut sync.Mutex
	var expectedOrder []string
	var actualOrder []string
	inUse := false

	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			setExpires(r.in.GetConnection(), time.Hour)

			mut.Lock()
			assert.False(t, inUse)
			inUse = true
			actualOrder = append(actualOrder, "request "+r.in.Connection.Context.ExtraContext["refresh"])
			mut.Unlock()

			time.Sleep(100 * time.Millisecond)

			mut.Lock()
			inUse = false
			mut.Unlock()
		},
		CloseFunc: func(r *testNSCClose) {
			mut.Lock()
			assert.False(t, inUse)
			inUse = true
			actualOrder = append(actualOrder, "close "+r.in.Context.ExtraContext["refresh"])
			mut.Unlock()

			time.Sleep(100 * time.Millisecond)

			mut.Lock()
			inUse = false
			mut.Unlock()
		},
	}
	client := next.NewNetworkServiceClient(
		serialize.NewClient(),
		refresh.NewClient(ctx),
		testRefresh,
	)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		req := mkRequest(0, i, nil)
		go func() {
			_, err := client.Request(context.Background(), req.Clone())
			assert.Nil(t, err)
			wg.Done()
		}()
		time.Sleep(10 * time.Millisecond)
		go func() {
			_, err := client.Close(context.Background(), req.Connection.Clone())
			assert.Nil(t, err)
			wg.Done()
		}()
		time.Sleep(10 * time.Millisecond)

		expectedOrder = append(expectedOrder,
			"request "+strconv.Itoa(i),
			"close "+strconv.Itoa(i),
		)
	}
	wg.Wait()

	assert.Equal(t, expectedOrder, actualOrder)
}

// TestRefreshClient_Parallel checks that requests/closes with a distinct ID are
// performed in parallel.
func TestRefreshClient_Parallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mut sync.Mutex
	expected := map[string]bool{}
	actual := map[string]bool{}
	connChs := []chan *networkservice.Connection{}

	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			time.Sleep(1 * time.Second)

			mut.Lock()
			actual["request "+r.in.Connection.Id] = true
			mut.Unlock()
		},
		CloseFunc: func(r *testNSCClose) {
			time.Sleep(1 * time.Second)

			mut.Lock()
			actual["close "+r.in.Id] = true
			mut.Unlock()
		},
	}
	client := next.NewNetworkServiceClient(
		serialize.NewClient(),
		refresh.NewClient(ctx),
		testRefresh,
	)

	for i := 0; i < 10; i++ {
		connCh := make(chan *networkservice.Connection, 1)
		connChs = append(connChs, connCh)
		req := mkRequest(i, 0, nil)
		go func() {
			conn, err := client.Request(context.Background(), req)
			assert.NotNil(t, conn)
			assert.Nil(t, err)
			connCh <- conn
		}()
		expected["request "+req.Connection.Id] = true
	}
	require.Eventually(t, func() bool {
		mut.Lock()
		defer mut.Unlock()
		return assert.ObjectsAreEqual(expected, actual)
	}, 2*time.Second, tickTimeout)

	for i := 0; i < 10; i++ {
		conn := <-connChs[i]
		go func() {
			_, err := client.Close(context.Background(), conn)
			assert.Nil(t, err)
		}()
		expected["close "+conn.Id] = true
	}
	require.Eventually(t, func() bool {
		mut.Lock()
		defer mut.Unlock()
		return assert.ObjectsAreEqual(expected, actual)
	}, 2*time.Second, tickTimeout)
}

func setExpires(conn *networkservice.Connection, expireTimeout time.Duration) {
	expireTime := time.Now().Add(expireTimeout)
	expires := &timestamp.Timestamp{
		Seconds: expireTime.Unix(),
		Nanos:   int32(expireTime.Nanosecond()),
	}
	conn.Path = &networkservice.Path{
		Index: 0,
		PathSegments: []*networkservice.PathSegment{
			{
				Expires: expires,
			},
		},
	}
}

// TODO: test connection cancel?
// TODO: test everything cancel?
// TODO: test connection updating by NSE
// TODO: test chain: ensure that refresh do not updates connection ids
