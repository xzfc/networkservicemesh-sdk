package refresh_test

import (
	"context"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

// TestClient_Serial checks that requests/closes with a same ID are performed sequentially.
func TestClient_Serial(t *testing.T) {
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
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		req := mkRequest(0, i, nil)
		go func(i int) {
			_, err := client.Request(context.Background(), req.Clone())
			assert.Nil(t, err)
			wg.Done()
		}(i)
		time.Sleep(10 * time.Millisecond)
		go func(i int) {
			_, err := client.Close(context.Background(), req.Connection)
			assert.Nil(t, err)
			wg.Done()
		}(i)
		time.Sleep(10 * time.Millisecond)

		expectedOrder = append(expectedOrder,
			"request "+strconv.Itoa(i),
			"close "+strconv.Itoa(i),
		)
	}
	wg.Wait()

	assert.Equal(t, expectedOrder, actualOrder)
}

// TestClient_Parallel checks that requests/closes with a distinct ID are performed in parallel.
func TestClient_Parallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mut sync.Mutex
	expected := map[string]bool{}
	actual := map[string]bool{}
	connChs := []chan *networkservice.Connection{}

	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			setExpires(r.in.GetConnection(), time.Hour)

			time.Sleep(1 * time.Second)

			mut.Lock()
			actual["request " + r.in.Connection.Id] = true
			mut.Unlock()
		},
		CloseFunc: func(r *testNSCClose) {
			time.Sleep(1 * time.Second)

			mut.Lock()
			actual["close " + r.in.Id] = true
			mut.Unlock()
		},
	}
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	for i := 0; i < 10; i++ {
		connCh := make(chan *networkservice.Connection, 1)
		connChs = append(connChs, connCh)
		req := mkRequest(i, 0, nil)
		go func(i int) {
			conn, err := client.Request(context.Background(), req)
			assert.NotNil(t, conn)
			assert.Nil(t, err)
			connCh <- conn
		}(i)
		expected["request " + req.Connection.Id] = true
	}
	require.Eventually(t, func() bool {
		mut.Lock()
		defer mut.Unlock()
		return assert.ObjectsAreEqual(expected, actual)
	}, 2 * time.Second, tickTimeout)


	for i := 0; i < 10; i++ {
		conn := <-connChs[i]
		go func(i int) {
			_, err := client.Close(context.Background(), conn)
			assert.Nil(t, err)
		}(i)
		expected["close " + conn.Id] = true
	}
	require.Eventually(t, func() bool {
		mut.Lock()
		defer mut.Unlock()
		return assert.ObjectsAreEqual(expected, actual)
	}, 2 * time.Second, tickTimeout)
}

// TODO: test connection cancel?
// TODO: test everything cancel?
// TODO: test connection updating by NSE
// TODO: test chain: ensure that refresh do not updates connection ids
