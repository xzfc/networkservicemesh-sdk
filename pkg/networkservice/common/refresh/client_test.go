// Copyright (c) 2020 Doc.ai and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package refresh_test

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/sdk/pkg/networkservice/common/refresh"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatepath"
	"github.com/networkservicemesh/sdk/pkg/networkservice/common/updatetoken"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/adapters"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/tools/sandbox"
)

const (
	expireTimeout        = 100 * time.Millisecond
	waitForTimeout       = expireTimeout
	tickTimeout          = 10 * time.Millisecond
	refreshCount         = 5
	expectAbsenceTimeout = 5 * expireTimeout

	requestNumber contextKeyType = "RequestNumber"
)

type contextKeyType string

func withRequestNumber(parent context.Context, number int) context.Context {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	return context.WithValue(parent, requestNumber, number)
}

func getRequestNumber(ctx context.Context) int {
	if rv, ok := ctx.Value(requestNumber).(int); ok {
		return rv
	}
	return -1
}

type testRefresh struct {
	RequestFunc func(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error)
}

func (t *testRefresh) Request(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	return t.RequestFunc(ctx, in, opts...)
}

func (t *testRefresh) Close(context.Context, *networkservice.Connection, ...grpc.CallOption) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

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

func hasValue(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func firstGetsValueEarlier(c1, c2 <-chan struct{}) bool {
	select {
	case <-c2:
		return false
	case <-c1:
		return true
	}
}

func TestNewClient_StopRefreshAtClose(t *testing.T) {
	// t.Skip("https://github.com/networkservicemesh/sdk/issues/237")
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	requestCh := make(chan struct{}, 1)
	testRefresh := &testRefresh{
		RequestFunc: func(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (connection *networkservice.Connection, err error) {
			setExpires(in.GetConnection(), expireTimeout)
			requestCh <- struct{}{}
			return in.GetConnection(), nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)
	request := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "conn-1",
		},
	}
	conn, err := client.Request(context.Background(), request)
	assert.Nil(t, err)

	assert.True(t, hasValue(requestCh)) // receive value from initial request
	for i := 0; i < refreshCount; i++ {
		require.Eventually(t, func() bool { return hasValue(requestCh) }, waitForTimeout, tickTimeout)
	}

	_, err = client.Close(context.Background(), conn)
	assert.Nil(t, err)

	absence := make(chan struct{})
	time.AfterFunc(expectAbsenceTimeout, func() {
		absence <- struct{}{}
	})
	assert.True(t, firstGetsValueEarlier(absence, requestCh))
}

func TestNewClient_StopRefreshAtAnotherRequest(t *testing.T) {
	// t.Skip("https://github.com/networkservicemesh/sdk/issues/260")
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	requestCh := make(chan struct{}, 1)
	testRefresh := &testRefresh{
		RequestFunc: func(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (connection *networkservice.Connection, err error) {
			setExpires(in.GetConnection(), expireTimeout)
			if getRequestNumber(ctx) == 1 {
				requestCh <- struct{}{}
			}
			return in.GetConnection(), nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	request1 := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "conn-1",
		},
	}
	_, err := client.Request(withRequestNumber(context.Background(), 1), request1)
	assert.Nil(t, err)

	assert.True(t, hasValue(requestCh)) // receive value from initial request
	for i := 0; i < refreshCount; i++ {
		require.Eventually(t, func() bool { return hasValue(requestCh) }, waitForTimeout, tickTimeout)
	}

	request2 := &networkservice.NetworkServiceRequest{
		Connection: &networkservice.Connection{
			Id: "conn-1",
		},
	}
	conn, err := client.Request(withRequestNumber(context.Background(), 2), request2)
	assert.Nil(t, err)

	absence := make(chan struct{})
	time.AfterFunc(expectAbsenceTimeout, func() {
		absence <- struct{}{}
	})
	assert.True(t, firstGetsValueEarlier(absence, requestCh))

	_, err = client.Close(context.Background(), conn)
	assert.Nil(t, err)
}


const (
	somethingExpire = 10 * time.Millisecond
	somethingDeadline = 20 * time.Millisecond
	somethingClientStep = 32 * time.Millisecond
)

func TestNewClient_Stress(t *testing.T) {
	// Race condition test.
	//
	// Time graph:
	//      time ->
	//   C: 0              1              2              ... 99
	//   R:  0  0  0  0  0  1  1  1  1  1  2  2  2  2  2 ...  99 99 99 99 99
	//
	// Legend:
	//   C - client requests
	//   R - requests sent by refreshClient
	//
	// Description:
	//   ...

	done := make(chan struct{})

	type event struct {
		typ    string
		time   time.Time
		number int
	}
	events := make(chan event, 1000)
	go func() {
		// This goroutine checks the following invariants
		// 
		// 1. The "recv" number either (a) doesn't change or (b) increments by 1.
		// 2. The last seen "recv" number should be either `expecting` or `expecting+1`.
		// 3. The number should be incremented in between "send-before" and "send-after" events.
		// 4. Each next number should be within somethingExpire.

		number := 0 // last seen "recv" number
		expecting := 0 // last seen "send-before" number
		seenNumber := false
		var last time.Time

		for event := range events {
			// fmt.Printf("%#v\n", event)
			switch event.typ {
			case "recv":
				if number == event.number && expecting == number {
					// OK: Number not changed.
				} else if number == event.number && expecting == number+1 {
					// OK: Number not changed. Expecting it to increment.
				} else if number + 1 == event.number && expecting == number + 1 {
					// OK: Number incremented.
					number = event.number
				} else {
					// Bad: invariant violation
					assert.Failf(t, "Invariant violation in \"recv\"",
						"number=%v expecting=%v event.number=%#v",
						number, expecting, event.number)
					done <- struct{}{}
					return
				}
				if seenNumber && last.Add(somethingDeadline).Before(event.time) {
					// Bad: invariant violation
					assert.Failf(t, "Invariant violation in \"recv\": time",
						"timeDelta=%.3fs",
						event.time.Sub(last).Seconds())
					done <- struct{}{}
					return
				}
				last = time.Now()
				seenNumber = true
			case "send-before":
				expecting = event.number
				if expecting != 0 && number + 1 != event.number {
					assert.Failf(t, "Invariant violation in \"send-before\"",
						"number=%v event=%#v",
						number, event)
					done <- struct{}{}
					return
				}
			case "send-end":
				if number != event.number {
					assert.Failf(t, "Invariant violation in \"send-end\"",
						"number=%v expecting=%v\nevent=%#v",
						number, expecting, event)
					done <- struct{}{}
					return
				}
			}
			_ = number
			_ = expecting
		}
	}()

	testRefresh := &testRefresh{
		RequestFunc: func(ctx context.Context, in *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (connection *networkservice.Connection, err error) {
			setExpires(in.GetConnection(), somethingExpire)

			events <- event {
				typ:  "recv",
				time: time.Now(),
				number: getRequestNumber(ctx),
			}

			// fmt.Printf("getRequestNumber(ctx) == %v\n", getRequestNumber(ctx))
			// TODO-01: return number 
			return in.GetConnection(), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

	//var conn *networkservice.Connection
	out:
	for i := 0; i < 1000; i++ {
		fmt.Printf("%v,", i)
		if i != 0 {
			select {
			case <-done: break out
			case <-time.After(somethingClientStep):
			}
		}
		events <- event {
			typ:  "send-before",
			time: time.Now(),
			number: i,
		}
	
		request := &networkservice.NetworkServiceRequest{
			Connection: &networkservice.Connection{
				Id: "conn-1",
				// TODO: set label value
				Labels: map[string]string{"0":"0"},
			},
		}

		conn, err := client.Request(
			withRequestNumber(context.Background(), i),
			request)
		events <- event {
			typ:  "send-done",
			time: time.Now(),
			number: i,
		}
		assert.Nil(t, err)
		// TODO-01: assert `conn.something == i`
		_ = conn
	}
}

func TestNewClient_Stress2(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	rand := rand.New(rand.NewSource(0))

	refreshSrv := newTestRefresh2(t, somethingExpire / 5, somethingExpire * 3 / 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(
		updatepath.NewClient("foo"),
		refresh.NewClient(ctx),
		updatetoken.NewClient(sandbox.GenerateExpiringToken(somethingExpire)),
		adapters.NewServerToClient(refreshSrv))

	closed := true
	for i := 0; i < 1000 && !t.Failed(); i++ {
		if i % 100 == 0 {
			fmt.Println()
		}
		if i % 10 == 0 {
			fmt.Printf("%v,", i)
		}

		refreshSrv.beforeRequest(strconv.Itoa(i))
		conn, err := client.Request(ctx, mkRequest(0, i))
		refreshSrv.afterRequest()
		assert.Nil(t, err)
		_ = conn
		closed = false

		if t.Failed() {
			break
		}

		if rand.Int31n(10) != 0 {
			time.Sleep(somethingClientStep)
		}

		if rand.Int31n(10) == 0 {
			refreshSrv.beforeClose()
			_, _ = client.Close(ctx, mkConn(0, i))
			refreshSrv.afterClose()
			closed = true
		}
	}
	fmt.Println()

	if !closed {
		refreshSrv.beforeClose()
		_, _ = client.Close(ctx, mkConn(0, 0))
		refreshSrv.afterClose()
	}
	time.Sleep(somethingClientStep)
}

func TestNewClient_Reuse(t *testing.T) {
	testRefresh := &testNSC{
		RequestFunc: func(r *testNSCRequest) {
			setExpires(r.in.GetConnection(), 10000 * time.Millisecond)
			time.Sleep(500 * time.Millisecond)
			fmt.Println("!!!")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := next.NewNetworkServiceClient(refresh.NewClient(ctx), testRefresh)

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
