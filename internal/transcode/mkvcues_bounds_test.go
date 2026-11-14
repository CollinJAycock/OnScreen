package transcode

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A child can be two bytes, and children used to be collected into a slice
// of 32-byte headers before any was looked at: 64 MiB of Cues became 1-2 GiB.
// Walking them must not allocate per child.
func TestMKVCueTimes_TinyChildrenDoNotAllocatePerChild(t *testing.T) {
	const voids = 1 << 20 // 2 MiB of 2-byte Void elements (EC 80)
	body := bytes.Repeat([]byte{0xEC, 0x80}, voids)
	body = append(body, cuePoint(0, 1)...)
	body = append(body, cuePoint(4000, 1)...)

	var times []uint64
	allocs := testing.AllocsPerRun(3, func() {
		var err error
		if times, err = mkvCueTimes(body, 1); err != nil {
			t.Fatal(err)
		}
	})
	if len(times) != 2 {
		t.Fatalf("times = %v, want 2 cue points", times)
	}
	if allocs > 64 {
		t.Errorf("walking %d children allocated %.0f times; want no per-child allocation", voids, allocs)
	}
}

func TestMKVCueTimes_RefusesTooManyCuePoints(t *testing.T) {
	// CuePoint{CueTime 1, CueTrackPositions{CueTrack 1}} with 1-byte sizes:
	// 10 bytes, so the over-cap Cues is ~10 MB.
	one := []byte{0xBB, 0x88, 0xB3, 0x81, 0x01, 0xB7, 0x83, 0xF7, 0x81, 0x01}
	body := bytes.Repeat(one, mkvMaxCuePoints+1)
	if _, err := mkvCueTimes(body, 1); err == nil || !strings.Contains(err.Error(), "cue points") {
		t.Fatalf("err = %v, want a cue-point cap refusal", err)
	}
	if got, err := mkvCueTimes(bytes.Repeat(one, 10), 1); err != nil || len(got) != 10 {
		t.Fatalf("10 cue points = (%d, %v), want (10, nil)", len(got), err)
	}
}

// Concurrent Starts on one file share a single Cues parse.
func TestLoadMKVKeyframes_ConcurrentCallersShareOneParse(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	prev := readMKVKeyframes
	readMKVKeyframes = func(string) ([]float64, error) {
		calls.Add(1)
		<-release
		return []float64{0, 4}, nil
	}
	t.Cleanup(func() { readMKVKeyframes = prev })

	mod := time.Unix(1700000000, 0)
	const callers = 8
	var wg sync.WaitGroup
	results := make(chan int, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kf, err := loadMKVKeyframes("/media/a.mkv", 123, mod)
			if err == nil {
				results <- len(kf)
			}
		}()
	}
	// Let every caller reach the in-flight load before it finishes.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	if n := calls.Load(); n != 1 {
		t.Errorf("%d concurrent callers parsed the Cues %d times, want 1", callers, n)
	}
	got := 0
	for n := range results {
		if n != 2 {
			t.Errorf("caller got %d keyframes, want 2", n)
		}
		got++
	}
	if got != callers {
		t.Errorf("%d of %d callers got a result", got, callers)
	}
	// A different file version is its own load.
	if _, err := loadMKVKeyframes("/media/a.mkv", 124, mod); err != nil || calls.Load() != 2 {
		t.Errorf("changed size reused the old load (calls=%d, err=%v)", calls.Load(), err)
	}
}

func TestMKVEachChild_RejectsOverrunAndUnknownSize(t *testing.T) {
	for name, body := range map[string][]byte{
		"overrun":      {0xBB, 0x85, 0x01}, // declares 5 bytes, has 1
		"unknown size": {0xBB, 0xFF},       // all value bits set
		"bad vint":     {0x00, 0x80},
		"truncated id": {0x1A},
	} {
		t.Run(name, func(t *testing.T) {
			if err := mkvEachChild(body, func(uint32, []byte) error { return nil }); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}
