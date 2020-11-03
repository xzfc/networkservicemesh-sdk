package refresh_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
	"github.com/networkservicemesh/sdk/pkg/networkservice/utils/null"
)

func testToken(_ credentials.AuthInfo) (tokenValue string, expireTime time.Time, err error) {
	expireTime = time.Now().UTC().Add(time.Millisecond * 100)
	return "TestToken", expireTime, nil
}

func testCreateChain(ctx context.Context, start networkservice.NetworkServiceClient) networkservice.NetworkServiceClient {
	client := start
	for i := 0; i < 10; i++ {
		server := chain.NewNetworkServiceServer(
			updatepath.NewServer("server-" + strconv.Itoa(i)),
			updatetoken.NewServer(testToken),
			adapters.NewClientToServer(client),
		)
		client = chain.NewNetworkServiceClient(
			updatepath.NewClient("client-" + strconv.Itoa(i)),
			refresh.NewClient(ctx),
			updatetoken.NewClient(testToken),
			adapters.NewServerToClient(server),
		)
		_ = refresh.NewClient
	}
	return client
}

func TestNope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initClient := null.NewClient()
	client := testCreateChain(ctx, initClient)

	request := mkRequest(0, 0)
	rv, err := client.Request(ctx, request.Clone())
	require.Nil(t, err)
	fmt.Println(rv)

	// request.Connection = rv
	// rv, err = client.Request(ctx, request.Clone())
	// require.Nil(t, err)
	// fmt.Println(rv)

	time.Sleep(time.Second*10)
}
