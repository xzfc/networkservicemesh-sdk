
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
