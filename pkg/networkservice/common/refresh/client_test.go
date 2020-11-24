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
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
)

const (
	expireTimeout     = 100 * time.Millisecond
	eventuallyTimeout = expireTimeout
	tickTimeout       = 10 * time.Millisecond
	neverTimeout      = 5 * expireTimeout
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

type stressTestConfig struct {
	name string
	expireTimeout time.Duration
	minDuration, maxDuration time.Duration
	tickDuration time.Duration
	iterations int
}

func TestRefreshClient_Stress(t *testing.T) {
	table := []stressTestConfig {
		{
			name:          "RaceConditions",
			expireTimeout: 2 * time.Millisecond,
			minDuration:   0,
			maxDuration:   time.Hour,
			tickDuration:  8100 * time.Microsecond,
			iterations:    100,
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

func runStressTest(t *testing.T, conf *stressTestConfig) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	randSrc := rand.New(rand.NewSource(0))

	refreshTester := newRefreshTesterServer(t, conf.minDuration, conf.maxDuration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(
		serialize.NewClient(),
		updatepath.NewClient("foo"),
		refresh.NewClient(ctx),
		updatetoken.NewClient(sandbox.GenerateExpiringToken(conf.expireTimeout)),
		adapters.NewServerToClient(refreshTester),
	)

	var oldConn *networkservice.Connection
	for i := 0; i < conf.iterations && !t.Failed(); i++ {
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

	if oldConn != nil {
		refreshTester.beforeClose()
		_, _ = client.Close(ctx, oldConn)
		refreshTester.afterClose()
	}
	time.Sleep(conf.tickDuration)
}
