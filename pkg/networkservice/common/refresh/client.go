package refresh

import (
	"context"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/trace"
)

type refreshClient struct {
	ctx   context.Context
	mutex sync.Mutex

	items          map[connectionID]*refreshItem
	timerIDs       map[timerID]connectionID
	timerIDCounter timerID
}

type connectionID string
type timerID int

type refreshItem struct {
	timer   *time.Timer
	timerID timerID
	// repeat is a request that should be performed when the timer fires.
	repeat *clientRequestItem

	// running represents an ongoing gRPC call. Nil if there is none.
	running queueItem

	// queue is an array of methods to run.
	queue []queueItem
}

type doneRequestItem struct {
	rv  *networkservice.Connection
	err error
}

type doneCloseItem struct {
	e   *empty.Empty
	err error
}

type clientRequestItem struct {
	ctx      context.Context
	req      *networkservice.NetworkServiceRequest
	grpcOpts []grpc.CallOption

	result *doneRequestItem
	// Non-nil during the first request, nil during the subsequent requests.
	resultCh []chan doneRequestItem
}

type clientCloseItem struct {
	ctx      context.Context
	conn     *networkservice.Connection
	grpcOpts []grpc.CallOption

	result   *doneCloseItem
	resultCh chan doneCloseItem
}

type queueItem interface {
	connectionID() connectionID
	// spawn a grpc call in a separate goroutine
	spawn(*refreshClient)
}

// NewClient - creates new NetworkServiceClient chain element for refreshing
// connections before they timeout at the endpoint
func NewClient1(ctx context.Context) networkservice.NetworkServiceClient {
	result := &refreshClient{
		ctx:            ctx,
		mutex:          sync.Mutex{},
		items:          map[connectionID]*refreshItem{},
		timerIDs:       map[timerID]connectionID{},
		timerIDCounter: 0,
	}

	return result
}

func (q *refreshClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	resultCh := make(chan doneRequestItem, 1)
	q.eventClient(&clientRequestItem{
		ctx:      ctx,
		req:      request.Clone(),
		grpcOpts: opts,
		resultCh: []chan doneRequestItem{resultCh},
	})
	result := <-resultCh
	return result.rv, result.err
}

func (q *refreshClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	resultCh := make(chan doneCloseItem, 1)
	q.eventClient(&clientCloseItem{
		ctx:      ctx,
		conn:     conn.Clone(),
		grpcOpts: opts,
		resultCh: resultCh,
	})
	result := <-resultCh
	return result.e, result.err
}

// event handling methods. General rules:
// * Each event handling method update the state of refreshClient. Think of it
//   as a state machine transition rules.
// * Each event handling method should be wrapped in mutex lock.
// * Event handling methods should return immediately. No blocking calls should
//   be performed inside. gRPC calls should be performed in separate goroutines.

func (q *refreshClient) eventClient(req queueItem) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	trace.Log(q.ctx).Infof("\x1b[32m●\x1b[m eventClient %T id=%#v", req, req.connectionID())
	id := req.connectionID()

	item, ok := q.items[id]
	if !ok {
		item = &refreshItem{
			timer:   nil,
			timerID: -1,
			repeat:  nil,
			running: nil,
			queue:   nil,
		}
		q.items[id] = item
	}

	// Cancel the timer
	if item.timer != nil {
		item.timer.Stop()
		item.timer = nil
		item.repeat = nil
		delete(q.timerIDs, item.timerID)
		item.timerID = -1
	}

	if !mergeRequests(q.ctx, item.running, req) {
		item.queue = append(item.queue, req)
		q.progressQueue(id)
	}
}

func (q *refreshClient) eventTimer(timerID timerID) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	trace.Log(q.ctx).Infof("\x1b[32m●\x1b[m eventTimer %v", timerID)
	id, ok := q.timerIDs[timerID]
	if !ok {
		// Got stale timer that is already cancelled.
		trace.Log(q.ctx).Infof("Got cancelled timer")
		return
	}
	delete(q.timerIDs, timerID)
	item, ok := q.items[id]
	if !ok {
		trace.Log(q.ctx).Panicf("Timer for non-existing item")
	}
	item.timer = nil
	item.timerID = -1
	// TODO: assert timerID == item.timerID?

	item.repeat.spawn(q)
	item.running = item.repeat
}

func (q *refreshClient) eventDone(id connectionID) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	trace.Log(q.ctx).Infof("\x1b[32m●\x1b[m eventDone id=%#v", id)
	item, ok := q.items[id]
	if !ok {
		trace.Log(q.ctx).Panicf("Done Request for non-existent item")
	}

	var it2 *doneRequestItem

	// Send response result to the client
	switch r := item.running.(type) {
	case *clientRequestItem:
		for _, resultCh := range r.resultCh {
			resultCh <- *r.result
		}

		if r.result.err != nil && r.resultCh == nil {
			// We've got an error on the first request.
			// We should not repeat this request.
			return
		}

		// resultCh should be nil after the first request
		r.resultCh = nil

		it2 = r.result
		r.result = nil
	case *clientCloseItem:
		r.resultCh <- *r.result
		r.result = nil
	case nil:
		trace.Log(q.ctx).Panicf("Done an unexpected request")
	default:
		trace.Log(q.ctx).Panicf("Unexpected type")
	}

	item.running = nil

	if len(item.queue) != 0 {
		trace.Log(q.ctx).Infof("progress: request")
		q.progressQueue(id)
	} else if item.repeat != nil {
		// XXX: debug stuff: disable refresh at all
		{ item.repeat = nil; return }

		// TODO: it.rv - is that right? Check for PR
		expireTime, err := ptypes.Timestamp(it2.rv.GetPath().GetPathSegments()[it2.rv.GetPath().GetIndex()].GetExpires())
		if err != nil {
			// TODO: handle it
			trace.Log(q.ctx).Panicf("invalid timestamp")
		}
		// TODO: introduce random noise into duration avoid timer lock
		duration := time.Until(expireTime) / 3
		trace.Log(q.ctx).Infof("duration=%v", duration)

		timerID := q.timerIDCounter
		q.timerIDCounter++

		item.timerID = timerID
		q.timerIDs[timerID] = id
		item.timer = time.AfterFunc(duration, func() {
			q.eventTimer(timerID)
		})
	}
}

// misc helpers

func mergeRequests(ctx context.Context, running queueItem, new_ queueItem) bool {
	runningReq, ok := running.(*clientRequestItem)
	if !ok {
		return false
	}
	newReq, ok := new_.(*clientRequestItem)
	if !ok {
		return false
	}

	// TODO: compare
	if false {
		return false
	}

	trace.Log(ctx).Infof("Merge requests")

	runningReq.resultCh = append(runningReq.resultCh, newReq.resultCh...)

	return true
}

func (q *refreshClient) progressQueue(id connectionID) {
	item := q.items[id]

	if len(item.queue) == 0 {
		trace.Log(q.ctx).Panicf("refreshClient: len(item.queue) == 0: should not happen")
	}

	if item.running == nil {
		item.running = item.queue[0]
		item.queue = item.queue[1:]

		switch r := item.running.(type) {
		case *clientRequestItem:
			r.spawn(q)
			item.repeat = r
		case *clientCloseItem:
			r.spawn(q)
			item.repeat = nil
		default:
			trace.Log(q.ctx).Panicf("Unexpected type: %T", r)
		}
	}
}

// queueItem implementations

func (it *clientRequestItem) connectionID() connectionID { return connectionID(it.req.Connection.Id) }
func (it *clientCloseItem) connectionID() connectionID   { return connectionID(it.conn.Id) }

func (it *clientRequestItem) spawn(q *refreshClient) {
	go func() {
		rv, err := next.Client(it.ctx).Request(it.ctx, it.req.Clone(), it.grpcOpts...)
		it.result = &doneRequestItem{rv: rv, err: err}
		q.eventDone(it.connectionID())
	}()
}
func (it *clientCloseItem) spawn(q *refreshClient) {
	go func() {
		e, err := next.Client(it.ctx).Close(it.ctx, it.conn.Clone(), it.grpcOpts...)
		it.result = &doneCloseItem{e: e, err: err}
		q.eventDone(it.connectionID())
	}()
}
