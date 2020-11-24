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
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
	"go.uber.org/goleak"
	"strconv"
	"testing"
	"time"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/null"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
	"github.com/stretchr/testify/require"
)

func createChain(ctx context.Context, start networkservice.NetworkServiceClient) networkservice.NetworkServiceClient {
	client := start
	for i := 0; i < 10; i++ {
		server := chain.NewNetworkServiceServer(
			updatepath.NewServer("server-"+strconv.Itoa(i)),
			updatetoken.NewServer(sandbox.GenerateExpiringToken(100 * time.Millisecond)),
			adapters.NewClientToServer(client),
		)
		client = chain.NewNetworkServiceClient(
			serialize.NewClient(),
			updatepath.NewClient("client-"+strconv.Itoa(i)),
			refresh.NewClient(ctx),
			updatetoken.NewClient(sandbox.GenerateExpiringToken(100 * time.Millisecond)),
			adapters.NewServerToClient(server),
		)
		_ = refresh.NewClient
	}
	return client
}

func TestRefreshClient_Chain(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initClient := null.NewClient()
	client := createChain(ctx, initClient)

	request := mkRequest(0, 0, nil)
	rv, err := client.Request(ctx, request.Clone())
	require.Nil(t, err)
	require.NotNil(t, rv)

	// request.Connection = rv
	// rv, err = client.Request(ctx, request.Clone())
	// require.Nil(t, err)
	// fmt.Println(rv)

	time.Sleep(time.Second * 1)
}
