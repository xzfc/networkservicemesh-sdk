package refresh_test

import (
	"context"
	"fmt"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/cls"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
)

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

func TestFoo(t *testing.T) {
	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			setExpires(r.in.GetConnection(), time.Millisecond*350)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	rv, err := client.Request(context.Background(), mkRequest(0, 0))
	fmt.Printf("Test: rv=%#v err=%#v\n", rv, err)
	time.Sleep(time.Second)

	rv, err = client.Request(context.Background(), mkRequest(0, 0))
	fmt.Printf("Test: rv=%#v err=%#v\n", rv, err)
	time.Sleep(time.Second)
}

// TestSerial checks that requests/closes with a same ID are performed sequentially.
func TestSerial(t *testing.T) {
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
		go func(i int) {
			_, err := client.Request(context.Background(), mkRequest(0, i))
			assert.Nil(t, err)
			wg.Done()
		}(i)
		time.Sleep(10 * time.Millisecond)
		go func(i int) {
			_, err := client.Close(context.Background(), mkConn(0, i))
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

// TestParallel checks that requests/closes with a distinct ID are performed in parallel.
func TestParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mut sync.Mutex
	expected := map[string]bool{}
	actual := map[string]bool{}

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
		go func(i int) {
			_, err := client.Request(context.Background(), mkRequest(i, 0))
			assert.Nil(t, err)
		}(i)
		expected["request " + mkConn(i, 0).Id] = true
	}
	require.Eventually(t, func() bool {
		mut.Lock()
		defer mut.Unlock()
		return assert.ObjectsAreEqual(expected, actual)
	}, 2 * time.Second, tickTimeout)


	for i := 0; i < 10; i++ {
		go func(i int) {
			_, err := client.Close(context.Background(), mkConn(i, 0))
			assert.Nil(t, err)
		}(i)
		expected["close " + mkConn(i, 0).Id] = true
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
