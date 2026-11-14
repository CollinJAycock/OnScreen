package media

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// One key's holders run one at a time; different keys don't wait on each
// other; and once nobody holds or waits for a key its entry is gone — the
// previous sync.Map kept a mutex for every title ever scanned.
func TestKeyedMutex_SerializesPerKeyAndFreesEntries(t *testing.T) {
	var k keyedMutex

	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := k.Lock("same")
			n := inside.Add(1)
			for {
				m := maxInside.Load()
				if n <= m || maxInside.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inside.Add(-1)
			unlock()
		}()
	}
	wg.Wait()
	if got := maxInside.Load(); got != 1 {
		t.Fatalf("%d holders of one key at once, want 1", got)
	}

	// A different key is not blocked by a held one.
	unlockA := k.Lock("a")
	done := make(chan struct{})
	go func() {
		k.Lock("b")()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("key b waited on key a")
	}
	unlockA()

	for i := 0; i < 1000; i++ {
		k.Lock(fmt.Sprint("title-", i))()
	}
	if n := k.size(); n != 0 {
		t.Fatalf("%d entries left after every lock was released, want 0", n)
	}
}
