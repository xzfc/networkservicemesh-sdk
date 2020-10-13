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

// Package nsmgr_test define a tests for NSMGR chain element.
package nsmgr_test

import (
	"context"
	"io/ioutil"
	"testing"
	"time"
	"fmt"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"go.uber.org/goleak"

	"github.com/stretchr/testify/require"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/cls"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	"github.com/networkservicemesh/api/pkg/api/registry"

	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
)

func TestNSMGR_RemoteUsecase(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	logrus.SetOutput(ioutil.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	domain := sandbox.NewBuilder(t).
		SetNodesCount(2).
		SetRegistryProxySupplier(nil).
		SetContext(ctx).
		Build()
	defer domain.Cleanup()

	nseReg := &registry.NetworkServiceEndpoint{
		Name:                "final-endpoint",
		NetworkServiceNames: []string{"my-service-remote"},
	}

	_, err := sandbox.NewEndpoint(ctx, nseReg, sandbox.GenerateTestToken, domain.Nodes[0].NSMgr)
	require.NoError(t, err)

	request := &networkservice.NetworkServiceRequest{
		MechanismPreferences: []*networkservice.Mechanism{
			{Cls: cls.LOCAL, Type: kernel.MECHANISM},
		},
		Connection: &networkservice.Connection{
			Id:             "1",
			NetworkService: "my-service-remote",
			Context:        &networkservice.ConnectionContext{},
		},
	}

	nsc, err := sandbox.NewClient(ctx, sandbox.GenerateTestToken, domain.Nodes[1].NSMgr.URL)
	require.NoError(t, err)

	conn, err := nsc.Request(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, 8, len(conn.Path.PathSegments))

	// Simulate refresh from client.

	refreshRequest := request.Clone()
	refreshRequest.GetConnection().Context = conn.Context
	refreshRequest.GetConnection().Mechanism = conn.Mechanism
	refreshRequest.GetConnection().NetworkServiceEndpointName = conn.NetworkServiceEndpointName

	conn, err = nsc.Request(ctx, refreshRequest)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, 8, len(conn.Path.PathSegments))
}

func TestNSMGR_LocalUsecase(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	logrus.SetOutput(ioutil.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	domain := sandbox.NewBuilder(t).
		SetNodesCount(1).
		SetContext(ctx).
		SetRegistryProxySupplier(nil).
		Build()
	defer domain.Cleanup()

	nseReg := &registry.NetworkServiceEndpoint{
		Name:                "final-endpoint",
		NetworkServiceNames: []string{"my-service-remote"},
	}
	_, err := sandbox.NewEndpoint(ctx, nseReg, sandbox.GenerateTestToken, domain.Nodes[0].NSMgr)
	require.NoError(t, err)

	nsc, err := sandbox.NewClient(ctx, sandbox.GenerateTestToken, domain.Nodes[0].NSMgr.URL)
	require.NoError(t, err)

	request := &networkservice.NetworkServiceRequest{
		MechanismPreferences: []*networkservice.Mechanism{
			{Cls: cls.LOCAL, Type: kernel.MECHANISM},
		},
		Connection: &networkservice.Connection{
			Id:             "1",
			NetworkService: "my-service-remote",
			Context:        &networkservice.ConnectionContext{},
		},
	}
	conn, err := nsc.Request(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, 5, len(conn.Path.PathSegments))

	// Simulate refresh from client.

	refreshRequest := request.Clone()
	refreshRequest.GetConnection().Context = conn.Context
	refreshRequest.GetConnection().Mechanism = conn.Mechanism
	refreshRequest.GetConnection().NetworkServiceEndpointName = conn.NetworkServiceEndpointName

	conn2, err := nsc.Request(ctx, refreshRequest)
	require.NoError(t, err)
	require.NotNil(t, conn2)
	require.Equal(t, 5, len(conn2.Path.PathSegments))
}

func TestNSMGR_Chain(t *testing.T) {
	nodesCount := 4

	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	logrus.SetOutput(ioutil.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	domain := sandbox.NewBuilder(t).
		SetNodesCount(nodesCount).
		SetContext(ctx).
		SetRegistryProxySupplier(nil).
		Build()
	defer domain.Cleanup()

	for i := 0; i < nodesCount; i++ {
		additionalFunctionality := []networkservice.NetworkServiceServer{}
		if i != 0 {
			// Passtrough to the node i-1
			prevNsc, err := sandbox.NewClient(
				ctx, sandbox.GenerateTestToken, domain.Nodes[i].NSMgr.URL,
				NewFwdClient(
					fmt.Sprintf("my-service-remote-%v", i-1),
					fmt.Sprintf("endpoint-%v", i-1),
				),
			)
			require.NoError(t, err)

			additionalFunctionality = []networkservice.NetworkServiceServer{
				adapters.NewClientToServer(prevNsc),
			}
		}
		nseReg := &registry.NetworkServiceEndpoint{
			Name:                fmt.Sprintf("endpoint-%v", i),
			NetworkServiceNames: []string{fmt.Sprintf("my-service-remote-%v", i)},
		}
		_, err := sandbox.NewEndpoint(ctx, nseReg, sandbox.GenerateTestToken, domain.Nodes[i].NSMgr, additionalFunctionality...)
		require.NoError(t, err)
	}

	nsc, err := sandbox.NewClient(ctx, sandbox.GenerateTestToken, domain.Nodes[nodesCount-1].NSMgr.URL)
	require.NoError(t, err)

	request := &networkservice.NetworkServiceRequest{
		MechanismPreferences: []*networkservice.Mechanism{
			{Cls: cls.LOCAL, Type: kernel.MECHANISM},
		},
		Connection: &networkservice.Connection{
			Id:             "1",
			NetworkService: fmt.Sprintf("my-service-remote-%v", nodesCount-1),
			Context:        &networkservice.ConnectionContext{},
		},
	}

	conn, err := nsc.Request(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, conn)
}


type FwdClient struct {
	networkService string
	networkServiceEndpointName string
}

func NewFwdClient(networkService, networkServiceEndpointName string) *FwdClient {
	return &FwdClient{
		networkService: networkService,
		networkServiceEndpointName: networkServiceEndpointName,
	}
}

func (q *FwdClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	fmt.Printf("Forwarding to -> %s\n", q.networkService)
	request = request.Clone()
	request.Connection.NetworkService = q.networkService
	request.Connection.NetworkServiceEndpointName = q.networkServiceEndpointName
	return next.Client(ctx).Request(ctx, request, opts...)
}

func (q *FwdClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	conn = conn.Clone()
	conn.NetworkService = q.networkService
	return next.Client(ctx).Close(ctx, conn, opts...)
}
