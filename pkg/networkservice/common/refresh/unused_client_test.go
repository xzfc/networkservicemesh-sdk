
// +build unused

package refresh_test

import (
	"context"
	"fmt"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/serialize"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

const requestNumber contextKeyType = "RequestNumber"
type contextKeyType string

// TODO: drop this test
func TestNewClient_Reuse(t *testing.T) {
	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			setExpires(r.in.GetConnection(), 10000*time.Millisecond)
			time.Sleep(500 * time.Millisecond)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(serialize.NewClient(), refresh.NewClient(ctx), testRefresh)

	request := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{Id: "conn"},
	}

	var conn1, conn2 *networkservice.Connection
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		conn1, err1 = client.Request(
			withRequestNumber(context.Background(), 1),
			request.Clone())
		wg.Done()
	}()
	time.Sleep(25 * time.Millisecond)
	go func() {
		conn2, err2 = client.Request(
			withRequestNumber(context.Background(), 1),
			request.Clone())
		wg.Done()
	}()
	wg.Wait()

	assert.Nil(t, err1)
	assert.Nil(t, err2)
	fmt.Println(conn1, conn2)
	// TODO: check that conn1 and conn2 are the same
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

// Utils:

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

func withRequestNumber(parent context.Context, number int) context.Context {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	return context.WithValue(parent, requestNumber, number)
}

// testNSC

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
