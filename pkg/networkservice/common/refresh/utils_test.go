package refresh_test

import (
	"context"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/cls"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	"strconv"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"google.golang.org/grpc"
)

type testNSC struct {
	RequestFunc func(r *testNSCRequest)
	CloseFunc func(r *testNSCClose)
}

type testNSCRequest struct {
	// Inputs
	ctx context.Context
	in *networkservice.NetworkServiceRequest
	opts []grpc.CallOption

	// Outputs
	conn *networkservice.Connection
	err error
}

type testNSCClose struct {
	// Inputs
	ctx context.Context
	in *networkservice.Connection
	opts []grpc.CallOption

	// Outputs
	e *empty.Empty
	err error
}

func (t *testNSC) Request(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	if t.RequestFunc != nil {
		r := &testNSCRequest{ctx, in, opts, in.GetConnection(), nil}
		t.RequestFunc(r)
		return r.conn, r.err
	} else {
		return in.GetConnection(), nil
	}
}

func (t *testNSC) Close(ctx context.Context, in *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	if t.CloseFunc != nil {
		r := &testNSCClose{ctx, in, opts, &empty.Empty{}, nil}
		t.CloseFunc(r)
		return r.e, r.err
	} else {
		return &empty.Empty{}, nil
	}
}

func mkConn(id int, label int) *networkservice.Connection {
	return &networkservice.Connection{
		Id:      "conn-" + strconv.Itoa(id),
		Context: &networkservice.ConnectionContext{
			ExtraContext: map[string]string{
				"refresh": strconv.Itoa(label),
			},
		},
		NetworkService: "my-service-remote",
	}
}

func mkRequest(id, label int) *networkservice.NetworkServiceRequest {
	return &networkservice.NetworkServiceRequest{
		MechanismPreferences: []*networkservice.Mechanism{
			{Cls: cls.LOCAL, Type: kernel.MECHANISM},
		},
		Connection: mkConn(id, label),
	}
}