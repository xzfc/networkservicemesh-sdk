
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

const (
	chainExpireTimeout = 500 * time.Millisecond
	chainMaxDuration   = 500 * time.Millisecond
	chainLength        = 10
	chainRequests      = 5
	chainStepDuration  = 1000 * time.Millisecond

	sandboxExpireTimeout = time.Second * 1
	sandboxStepDuration  = time.Second * 2
	sandboxRequests      = 4
	sandboxTotalTimeout  = time.Second * 15
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


////
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
			maxDuration:   maxDuration,
			tickDuration:  8100 * time.Microsecond,
			iterations:    100,
		},
		{
			name: "Durations",
			expireTimeout: 500 * time.Millisecond,
			minDuration:   500 * time.Millisecond / 5,
			maxDuration:   500 * time.Millisecond,
			tickDuration:  409 * time.Millisecond,
			iterations:    15,
		},
	}
	for _, q := range table {
		t.Run(q.name, func(t *testing.T) { runStressTest(t, &q) })
	}
}

func runStressTest(t *testing.T, conf *stressTestConfig) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

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

	generateRequests(t, client, refreshTester, conf.iterations, conf.tickDuration)
}

func TestRefreshClient_Chain(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	refreshTester := newRefreshTesterServer(t, 0, chainMaxDuration)
	client := adapters.NewServerToClient(refreshTester)
	for i := 0; i < chainLength; i++ {
		server := chain.NewNetworkServiceServer(
			updatepath.NewServer("server-"+strconv.Itoa(i)),
			updatetoken.NewServer(sandbox.GenerateExpiringToken(chainExpireTimeout)),
			adapters.NewClientToServer(client),
		)
		client = chain.NewNetworkServiceClient(
			serialize.NewClient(),
			updatepath.NewClient("client-"+strconv.Itoa(i)),
			refresh.NewClient(ctx),
			updatetoken.NewClient(sandbox.GenerateExpiringToken(chainExpireTimeout)),
			adapters.NewServerToClient(server),
		)
		_ = refresh.NewClient
	}

	generateRequests(t, client, refreshTester, chainRequests, chainStepDuration)
}

func TestRefreshClient_Sandbox(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	logrus.SetLevel(logrus.TraceLevel)
	// logrus.SetOutput(ioutil.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), sandboxTotalTimeout)
	defer cancel()

	tokenGenerator := sandbox.GenerateExpiringToken(sandboxExpireTimeout)

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

	refreshSrv := newRefreshTesterServer(t, 0, sandboxExpireTimeout)
	_, err := sandbox.NewEndpoint(ctx, nseReg, tokenGenerator, domain.Nodes[0].NSMgr, refreshSrv)
	require.NoError(t, err)

	nsc := sandbox.NewClient(ctx, tokenGenerator, domain.Nodes[1].NSMgr.URL)

	generateRequests(t, nsc, refreshSrv, sandboxRequests, sandboxStepDuration)
}
