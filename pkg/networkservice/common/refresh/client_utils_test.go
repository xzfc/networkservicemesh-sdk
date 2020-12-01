package refresh_test

import (
	"context"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countClient struct {
	t     *testing.T
	count int32
}

func (c *countClient) validator(atLeast int32) func() bool {
	return func() bool {
		if count := atomic.LoadInt32(&c.count); count < atLeast {
			logrus.Warnf("count %v < atLeast %v", count, atLeast)
			return false
		}
		return true
	}
}

func (c *countClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	request = request.Clone()
	conn := request.GetConnection()

	// Check that refresh updates the request.Connection field (issue #530).
	if atomic.AddInt32(&c.count, 1) == 1 {
		conn.NetworkServiceEndpointName = endpointName
	} else {
		require.Equal(c.t, endpointName, conn.NetworkServiceEndpointName)
	}

	return next.Client(ctx).Request(ctx, request, opts...)
}

func (c *countClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	return next.Client(ctx).Close(ctx, conn, opts...)
}

// refreshTestServer is a helper endpoint to check that the Request()/Close()
// order isn't mixed by refresh (which may happen due to race condition between
// a requests initiated by a client, and a refresh timer), and timer initiated
// request aren't too fast or too slow.
//
// Usage details:
// * Each client Request() should be wrapped in beforeRequest()/afterRequest()
//   calls. Same for Close() and beforeClose()/afterClose().
// * Caveat: parallel client initiated requests aren't supported by this tester.
// * To distinguish between different requests, the value of
//   `Connection.Context.ExtraContext["refresh"]` is used as a marker.
type refreshTesterServer struct {
	t           *testing.T
	minDuration time.Duration
	maxDuration time.Duration

	mutex         sync.Mutex
	state         refreshTesterServerState
	lastSeen      time.Time
	currentMarker string
	nextMarker    string
}

type refreshTesterServerState string

const (
	testRefreshStateInit        = refreshTesterServerState("init")
	testRefreshStateWaitRequest = refreshTesterServerState("wait-request")
	testRefreshStateDoneRequest = refreshTesterServerState("done-request")
	testRefreshStateRunning	    = refreshTesterServerState("running")
	testRefreshStateWaitClose	= refreshTesterServerState("wait-close")
)

func newRefreshTesterServer(t *testing.T, minDuration, maxDuration time.Duration) *refreshTesterServer {
	return &refreshTesterServer{
		t:           t,
		minDuration: minDuration,
		maxDuration: maxDuration,
		state:       testRefreshStateInit,
	}
}

func (t *refreshTesterServer) beforeRequest(marker string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Contains(t.t, []refreshTesterServerState{testRefreshStateInit, testRefreshStateRunning}, t.state, "Unexpected state")
	t.state = testRefreshStateWaitRequest
	t.nextMarker = marker
}

func (t *refreshTesterServer) afterRequest() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateDoneRequest, t.state, "Unexpected state")
	t.state = testRefreshStateRunning
	t.currentMarker = t.nextMarker
}

func (t *refreshTesterServer) beforeClose() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateRunning, t.state, "Unexpected state")
	t.state = testRefreshStateWaitClose
}

func (t *refreshTesterServer) afterClose() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateWaitClose, t.state, "Unexpected state")
	t.state = testRefreshStateInit
	t.currentMarker = ""
}

func (t *refreshTesterServer) checkUnlocked() {
	if t.state == testRefreshStateDoneRequest || t.state == testRefreshStateRunning {
		delta := time.Now().UTC().Sub(t.lastSeen)
		require.Less(t.t, int64(delta), int64(t.maxDuration), "Duration expired (too slow) delta=%v max=%v", delta, t.maxDuration)
	}
}

func (t *refreshTesterServer) Request(_ context.Context, request *networkservice.NetworkServiceRequest) (*networkservice.Connection, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()

	marker, _ := request.Connection.Context.ExtraContext["refresh"]
	require.NotEmpty(t.t, marker, "Marker is empty")

	switch t.state {
	case testRefreshStateWaitRequest:
		require.Contains(t.t, []string{t.nextMarker, t.currentMarker}, marker, "Unexpected marker")
		if marker == t.nextMarker {
			t.state = testRefreshStateDoneRequest
		}
	case testRefreshStateDoneRequest, testRefreshStateRunning, testRefreshStateWaitClose:
		require.Equal(t.t, t.currentMarker, marker, "Unexpected marker")
		require.Greater(t.t, int64(time.Now().UTC().Sub(t.lastSeen)), int64(t.minDuration), "Too fast")
	default:
		require.Fail(t.t, "Unexpected state", t.state)
	}

	t.lastSeen = time.Now()
	return request.Connection, nil
}

func (t *refreshTesterServer) Close(ctx context.Context, connection *networkservice.Connection) (*empty.Empty, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()

	// TODO: check for closes

	return &empty.Empty{}, nil
}
