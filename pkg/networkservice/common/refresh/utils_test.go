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
	"strconv"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/cls"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	"google.golang.org/grpc"
)

type testNSC struct {
	RequestFunc func(r *testNSCRequest)
	CloseFunc   func(r *testNSCClose)
}

type testNSCRequest struct {
	// Inputs
	ctx  context.Context
	in   *networkservice.NetworkServiceRequest
	opts []grpc.CallOption

	// Outputs
	conn *networkservice.Connection
	err  error
}

type testNSCClose struct {
	// Inputs
	ctx  context.Context
	in   *networkservice.Connection
	opts []grpc.CallOption

	// Outputs
	e   *empty.Empty
	err error
}

func (t *testNSC) Request(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	if t.RequestFunc != nil {
		r := &testNSCRequest{ctx, in, opts, in.GetConnection(), nil}
		t.RequestFunc(r)
		return r.conn, r.err
	}
	return in.GetConnection(), nil
}

func (t *testNSC) Close(ctx context.Context, in *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	if t.CloseFunc != nil {
		r := &testNSCClose{ctx, in, opts, &empty.Empty{}, nil}
		t.CloseFunc(r)
		return r.e, r.err
	}
	return &empty.Empty{}, nil
}

func mkRequest(id, marker int, conn *networkservice.Connection) *networkservice.NetworkServiceRequest {
	if conn == nil {
		conn = &networkservice.Connection{
			Id: "conn-" + strconv.Itoa(id),
			Context: &networkservice.ConnectionContext{
				ExtraContext: map[string]string{
					"refresh": strconv.Itoa(marker),
				},
			},
			NetworkService: "my-service-remote",
		}
	} else {
		conn.Context.ExtraContext["refresh"] = strconv.Itoa(marker)
	}
	return &networkservice.NetworkServiceRequest{
		MechanismPreferences: []*networkservice.Mechanism{
			{Cls: cls.LOCAL, Type: kernel.MECHANISM},
		},
		Connection: conn,
	}
}
