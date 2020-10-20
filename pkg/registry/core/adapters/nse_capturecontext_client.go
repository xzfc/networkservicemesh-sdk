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

package adapters

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/registry"
	"google.golang.org/grpc"
)

type contextNSEClient struct{}

func (c *contextNSEClient) Register(ctx context.Context, in *registry.NetworkServiceEndpoint, opts ...grpc.CallOption) (*registry.NetworkServiceEndpoint, error) {
	ctx2 := ctx.Value(serverContextKey).(*nextServerCtx)
	return ctx2.next.Register(context.WithValue(ctx, serverContextKey, ctx2.old), in)
}

type something struct {
	registry.NetworkServiceRegistry_FindClient
}

func (s something) Recv() (*registry.NetworkServiceEndpoint, error) {
	panic("implement me")
}

func (c *contextNSEClient) Find(ctx context.Context, in *registry.NetworkServiceEndpointQuery, opts ...grpc.CallOption) (registry.NetworkServiceEndpointRegistry_FindClient, error) {
	ctx2 := ctx.Value(serverContextKey).(*nextServerCtx)

	s := &something{}

	wtf := context.WithValue(ctx, serverContextKey, ctx2.old)
	_ = wtf

	_ = ctx2.next.Find(in, s)
	return s, nil
}

func (c *contextNSEClient) Unregister(ctx context.Context, in *registry.NetworkServiceEndpoint, opts ...grpc.CallOption) (*empty.Empty, error) {
	ctx2 := ctx.Value(serverContextKey).(*nextServerCtx)
	return ctx2.next.Unregister(context.WithValue(ctx, serverContextKey, ctx2.old), in)
}

var _ registry.NetworkServiceEndpointRegistryClient = &contextNSEClient{}
