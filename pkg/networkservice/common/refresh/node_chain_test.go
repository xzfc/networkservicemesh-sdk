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
	"strconv"
	"testing"
	"time"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/null"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
)

func testToken(_ credentials.AuthInfo) (tokenValue string, expireTime time.Time, err error) {
	expireTime = time.Now().UTC().Add(time.Millisecond * 100)
	return "TestToken", expireTime, nil
}

func testCreateChain(ctx context.Context, start networkservice.NetworkServiceClient) networkservice.NetworkServiceClient {
	client := start
	for i := 0; i < 10; i++ {
		server := chain.NewNetworkServiceServer(
			updatepath.NewServer("server-"+strconv.Itoa(i)),
			updatetoken.NewServer(testToken),
			adapters.NewClientToServer(client),
		)
		client = chain.NewNetworkServiceClient(
			serialize.NewClient(),
			updatepath.NewClient("client-"+strconv.Itoa(i)),
			refresh.NewClient(ctx),
			updatetoken.NewClient(testToken),
			adapters.NewServerToClient(server),
		)
		_ = refresh.NewClient
	}
	return client
}

func TestNope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initClient := null.NewClient()
	client := testCreateChain(ctx, initClient)

	request := mkRequest(0, 0, nil)
	rv, err := client.Request(ctx, request.Clone())
	require.Nil(t, err)
	fmt.Println(rv)

	// request.Connection = rv
	// rv, err = client.Request(ctx, request.Clone())
	// require.Nil(t, err)
	// fmt.Println(rv)

	time.Sleep(time.Second * 10)
}
