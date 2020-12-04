package refresh_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const atOnce = 15
const duration = 15 * time.Millisecond
const iterations = 1000

func TestTimers(t *testing.T) {
	var outliers int32
	var maxDelta time.Duration
	var mu sync.Mutex

	for j := 0; j < iterations; j++ {
		fmt.Printf("Batch %v:", j)

		var wg sync.WaitGroup
		wg.Add(atOnce)

		for i := 0; i < atOnce; i++ {
			start := time.Now()
			duration2 := duration + (time.Duration(i) * time.Millisecond)
			time.AfterFunc(duration2, func() {
				delta := time.Now().Sub(start.Add(duration2))
				if delta >= time.Millisecond {
					fmt.Printf(" %v", delta)
					atomic.AddInt32(&outliers, 1)
					mu.Lock()
					if delta > maxDelta {
						maxDelta = delta
					}
					mu.Unlock()
				}
				wg.Done()
			})
		}
		wg.Wait()
		fmt.Printf("\n")
	}

	fmt.Printf("Total outliers: %v / %v\n", outliers, iterations * atOnce)
	fmt.Printf("Max delta = %v\n", maxDelta)
	if outliers != 0 {
		t.Fail()
	}
}
