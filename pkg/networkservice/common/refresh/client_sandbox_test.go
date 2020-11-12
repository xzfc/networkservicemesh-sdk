package refresh_test

import (
	"context"
	"github.com/networkservicemesh/api/pkg/api/registry"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"io/ioutil"
	"testing"
	"time"
)

func TestClient_Sandbox(t *testing.T) {
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

	refreshSrv := newRefreshTesterServer(t, time.Millisecond * 100, time.Millisecond * 500)
	_, err := sandbox.NewEndpoint(ctx, nseReg, tokenGenerator, domain.Nodes[0].NSMgr, refreshSrv)
	require.NoError(t, err)

	nsc, err := sandbox.NewClient(ctx, tokenGenerator, domain.Nodes[1].NSMgr.URL)
	require.NoError(t, err)

	refreshSrv.beforeRequest("0")
	conn, err := nsc.Request(ctx, mkRequest(0, 0, nil))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 5)

	refreshSrv.beforeRequest("1")
	conn, err = nsc.Request(ctx, mkRequest(0, 1, conn.Clone()))
	refreshSrv.afterRequest()
	require.NoError(t, err)
	require.NotNil(t, conn)

	time.Sleep(time.Second * 1)

	refreshSrv.beforeClose()
	_, err = nsc.Close(ctx, conn.Clone())
	refreshSrv.afterClose()
	time.Sleep(time.Millisecond * 100)
}

