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
	"io/ioutil"
	"testing"
	"time"

	"github.com/networkservicemesh/api/pkg/api/registry"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
)

func TestRefreshClient_Sandbox(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	logrus.SetOutput(ioutil.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	tokenGenerator := sandbox.GenerateExpiringToken(time.Second * 5)

	domain := sandbox.NewBuilder(t).
		SetNodesCount(2).
		SetContext(ctx).
		SetRegistryProxySupplier(nil).
		SetTokenGenerateFunc(tokenGenerator).
		Build()
	defer domain.Cleanup()

	nseReg := &registry.NetworkServiceEndpoint{
		Name:                "final-endpoint",
		NetworkServiceNames: []string{"my-service-remote"},
	}

	refreshSrv := newRefreshTesterServer(t, time.Second*1, time.Second*5)
	_, err := sandbox.NewEndpoint(ctx, nseReg, tokenGenerator, domain.Nodes[0].NSMgr, refreshSrv)
	require.NoError(t, err)

	nsc, err := sandbox.NewClient(ctx, tokenGenerator, domain.Nodes[1].NSMgr.URL)
	require.NoError(t, err)

	refreshSrv.beforeRequest("0")
	conn, err := nsc.Request(ctx, mkRequest(0, nil))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 5)

	refreshSrv.beforeRequest("1")
	conn, err = nsc.Request(ctx, mkRequest(1, conn.Clone()))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 5)

	refreshSrv.beforeRequest("2")
	conn, err = nsc.Request(ctx, mkRequest(2, conn.Clone()))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 5)

	refreshSrv.beforeClose()
	_, err = nsc.Close(ctx, conn.Clone())
	refreshSrv.afterClose()
	require.NoError(t, err)
	time.Sleep(time.Second * 5)
}
