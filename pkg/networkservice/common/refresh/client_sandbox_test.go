package refresh_test

import (
	"context"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/api/pkg/api/registry"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"io/ioutil"
	"sync"
	"testing"
	"time"
)

type testRefresh2 struct {
	t           *testing.T
	minDuration time.Duration
	maxDuration time.Duration

	mutex       sync.Mutex
	state       int
	lastSeen    time.Time
	value       string
	switchingTo string
}

const (
	testRefreshStateInit = iota
	testRefreshStateWaitRequest
	testRefreshStateDoneRequest
	testRefreshStateRunning
	testRefreshStateWaitClose
)

func newTestRefresh2(t *testing.T, minDuration, maxDuration time.Duration) *testRefresh2 {
	return &testRefresh2{
		t: t,
		minDuration: minDuration,
		maxDuration: maxDuration,
		state: testRefreshStateInit,
	}
}
func (t *testRefresh2) beforeRequest(val string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Contains(t.t, []int{testRefreshStateInit, testRefreshStateRunning}, t.state, "Unexpected state")
	t.state = testRefreshStateWaitRequest
	t.switchingTo = val
}
func (t *testRefresh2) afterRequest() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateDoneRequest, t.state, "Unexpected state")
	t.state = testRefreshStateRunning
	t.value = t.switchingTo
}
func (t *testRefresh2) beforeClose() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateRunning, t.state, "Unexpected state")
	t.state = testRefreshStateWaitClose
}
func (t *testRefresh2) afterClose() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()
	require.Equal(t.t, testRefreshStateWaitClose, t.state, "Unexpected state")
	t.state = testRefreshStateInit
	t.value = ""
}
func (t *testRefresh2) checkUnlocked() {
	if t.state == testRefreshStateDoneRequest || t.state == testRefreshStateRunning {
		delta := time.Now().UTC().Sub(t.lastSeen)
		require.Less(t.t, int64(delta), int64(t.maxDuration), "Duration expired delta=%v max=%v", delta, t.maxDuration)
	}
}
func (t *testRefresh2) Request(_ context.Context, request *networkservice.NetworkServiceRequest) (*networkservice.Connection, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()

	value := request.Connection.Context.ExtraContext["refresh"]
	require.NotEmpty(t.t, value, "Value")

	switch t.state {
	case testRefreshStateWaitRequest:
		require.Contains(t.t, []string{t.switchingTo, t.value}, value, "Unexpected value")
		if value == t.switchingTo {
			t.state = testRefreshStateDoneRequest
		}
	case testRefreshStateDoneRequest, testRefreshStateRunning, testRefreshStateWaitClose:
		require.Equal(t.t, t.value, value, "Unexpected value")
		require.Greater(t.t, int64(time.Now().UTC().Sub(t.lastSeen)), int64(t.minDuration), "Too fast")
	default:
		require.Fail(t.t, "Unexpected state", t.state)
	}

	t.lastSeen = time.Now()
	return request.Connection, nil
}
func (t *testRefresh2) Close(ctx context.Context, connection *networkservice.Connection) (*empty.Empty, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.checkUnlocked()

	return &empty.Empty{}, nil
}

func TestSandbox(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	logrus.SetOutput(ioutil.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	tokenGenerator := sandbox.GenerateExpiringToken(time.Millisecond * 500)

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

	refreshSrv := newTestRefresh2(t, time.Millisecond * 100, time.Millisecond * 500)
	_, err := sandbox.NewEndpoint(ctx, nseReg, tokenGenerator, domain.Nodes[0].NSMgr, refreshSrv)
	require.NoError(t, err)

	nsc, err := sandbox.NewClient(ctx, tokenGenerator, domain.Nodes[1].NSMgr.URL)
	require.NoError(t, err)

	refreshSrv.beforeRequest("0")
	conn, err := nsc.Request(ctx, mkRequest(0, 0))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 5)

	refreshSrv.beforeRequest("1")
	conn, err = nsc.Request(ctx, mkRequest(0, 1))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 1)

	refreshSrv.beforeClose()
	_, err = nsc.Close(ctx, mkConn(0, 1))
	refreshSrv.afterClose()
	time.Sleep(time.Millisecond * 100)
}

