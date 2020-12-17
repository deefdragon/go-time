package rate

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenBucketBurst1(t *testing.T) {
	run(t, NewTokenBucket(10, 1), []allow{
		{t0, 1, true},
		{t0, 1, false},
		{t0, 1, false},
		{t1, 1, true},
		{t1, 1, false},
		{t1, 1, false},
		{t2, 2, false}, // burst size is 1, so n=2 always fails
		{t2, 1, true},
		{t2, 1, false},
	})
}

func TestTokenBucketBurst3(t *testing.T) {
	run(t, NewTokenBucket(10, 3), []allow{
		{t0, 2, true},
		{t0, 2, false},
		{t0, 1, true},
		{t0, 1, false},
		{t1, 4, false},
		{t2, 1, true},
		{t3, 1, true},
		{t4, 1, true},
		{t4, 1, true},
		{t4, 1, false},
		{t4, 1, false},
		{t9, 3, true},
		{t9, 0, true},
	})
}

func TestTokenBucketJumpBackwards(t *testing.T) {
	run(t, NewTokenBucket(10, 3), []allow{
		{t1, 1, true}, // start at t1
		{t0, 1, true}, // jump back to t0, two tokens remain
		{t0, 1, true},
		{t0, 1, false},
		{t0, 1, false},
		{t1, 1, true}, // got a token
		{t1, 1, false},
		{t1, 1, false},
		{t2, 1, true}, // got another token
		{t2, 1, false},
		{t2, 1, false},
	})
}

// Ensure that tokensFromDuration doesn't produce
// rounding errors by truncating nanoseconds.
// See golang.org/issues/34861.
func TestTokenBucket_noTruncationErrors(t *testing.T) {
	if !NewTokenBucket(0.7692307692307693, 1).Allow() {
		t.Fatal("expected true")
	}
}

func TestTokenBucketSimultaneousRequests(t *testing.T) {
	const (
		limit       = 1
		burst       = 5
		numRequests = 15
	)
	var (
		wg    sync.WaitGroup
		numOK = uint32(0)
	)

	// Very slow replenishing bucket.
	lim := NewTokenBucket(limit, burst)

	// Tries to take a token, atomically updates the counter and decreases the wait
	// group counter.
	f := func() {
		defer wg.Done()
		if ok := lim.Allow(); ok {
			atomic.AddUint32(&numOK, 1)
		}
	}

	wg.Add(numRequests)
	for i := 0; i < numRequests; i++ {
		go f()
	}
	wg.Wait()
	if numOK != burst {
		t.Errorf("numOK = %d, want %d", numOK, burst)
	}
}

func TestTokenBucketLongRunningQPS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "openbsd" {
		t.Skip("low resolution time.Sleep invalidates test (golang.org/issue/14183)")
		return
	}

	// The test runs for a few seconds executing many requests and then checks
	// that overall number of requests is reasonable.
	const (
		limit = 100
		burst = 100
	)
	var numOK = int32(0)

	lim := NewTokenBucket(limit, burst)

	var wg sync.WaitGroup
	f := func() {
		if ok := lim.Allow(); ok {
			atomic.AddInt32(&numOK, 1)
		}
		wg.Done()
	}

	start := time.Now()
	end := start.Add(5 * time.Second)
	for time.Now().Before(end) {
		wg.Add(1)
		go f()

		// This will still offer ~500 requests per second, but won't consume
		// outrageous amount of CPU.
		time.Sleep(2 * time.Millisecond)
	}
	wg.Wait()
	elapsed := time.Since(start)
	ideal := burst + (limit * float64(elapsed) / float64(time.Second))

	// We should never get more requests than allowed.
	if want := int32(ideal + 1); numOK > want {
		t.Errorf("numOK = %d, want %d (ideal %f)", numOK, want, ideal)
	}
	// We should get very close to the number of requests allowed.
	if want := int32(0.999 * ideal); numOK < want {
		t.Errorf("numOK = %d, want %d (ideal %f)", numOK, want, ideal)
	}
}

func TestTokenBucketSimpleReserve(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	runTokenBucketReserve(t, lim, request{t0, 2, t2, true})
	runTokenBucketReserve(t, lim, request{t3, 2, t4, true})
}

func TestTokenBucketMix(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 3, t1, false}) // should return false because n > Burst
	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	run(t, lim, []allow{{t1, 2, false}}) // not enought tokens - don't allow
	runTokenBucketReserve(t, lim, request{t1, 2, t2, true})
	run(t, lim, []allow{{t1, 1, false}}) // negative tokens - don't allow
	run(t, lim, []allow{{t3, 1, true}})
}

func TestTokenBucketCancelInvalid(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	r := runTokenBucketReserve(t, lim, request{t0, 3, t3, false})
	r.CancelAt(t0)                                          // should have no effect
	runTokenBucketReserve(t, lim, request{t0, 2, t2, true}) // did not get extra tokens
}

func TestTokenBucketCancelLast(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	r := runTokenBucketReserve(t, lim, request{t0, 2, t2, true})
	r.CancelAt(t1) // got 2 tokens back
	runTokenBucketReserve(t, lim, request{t1, 2, t2, true})
}

func TestTokenBucketCancelTooLate(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	r := runTokenBucketReserve(t, lim, request{t0, 2, t2, true})
	r.CancelAt(t3) // too late to cancel - should have no effect
	runTokenBucketReserve(t, lim, request{t3, 2, t4, true})
}

func TestTokenBucketCancel0Tokens(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	r := runTokenBucketReserve(t, lim, request{t0, 1, t1, true})
	runTokenBucketReserve(t, lim, request{t0, 1, t2, true})
	r.CancelAt(t0) // got 0 tokens back
	runTokenBucketReserve(t, lim, request{t0, 1, t3, true})
}

func TestTokenBucketCancel1Token(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	r := runTokenBucketReserve(t, lim, request{t0, 2, t2, true})
	runTokenBucketReserve(t, lim, request{t0, 1, t3, true})
	r.CancelAt(t2) // got 1 token back
	runTokenBucketReserve(t, lim, request{t2, 2, t4, true})
}

func TestTokenBucketCancelMulti(t *testing.T) {
	lim := NewTokenBucket(10, 4)

	runTokenBucketReserve(t, lim, request{t0, 4, t0, true})
	rA := runTokenBucketReserve(t, lim, request{t0, 3, t3, true})
	runTokenBucketReserve(t, lim, request{t0, 1, t4, true})
	rC := runTokenBucketReserve(t, lim, request{t0, 1, t5, true})
	rC.CancelAt(t1) // get 1 token back
	rA.CancelAt(t1) // get 2 tokens back, as if C was never reserved
	runTokenBucketReserve(t, lim, request{t1, 3, t5, true})
}

func TestTokenBucketReserveJumpBack(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t1, 2, t1, true}) // start at t1
	runTokenBucketReserve(t, lim, request{t0, 1, t1, true}) // should violate Limit,Burst
	runTokenBucketReserve(t, lim, request{t2, 2, t3, true})
}

func TestTokenBucketReserveJumpBackCancel(t *testing.T) {
	lim := NewTokenBucket(10, 2)

	runTokenBucketReserve(t, lim, request{t1, 2, t1, true}) // start at t1
	r := runTokenBucketReserve(t, lim, request{t1, 2, t3, true})
	runTokenBucketReserve(t, lim, request{t1, 1, t4, true})
	r.CancelAt(t0)                                          // cancel at t0, get 1 token back
	runTokenBucketReserve(t, lim, request{t1, 2, t4, true}) // should violate Limit,Burst
}

func TestTokenBucketReserveSetLimit(t *testing.T) {
	lim := NewTokenBucket(5, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	runTokenBucketReserve(t, lim, request{t0, 2, t4, true})
	lim.SetLimitAt(t2, 10)
	runTokenBucketReserve(t, lim, request{t2, 1, t4, true}) // violates Limit and Burst
}

func TestTokenBucketReserveSetBurst(t *testing.T) {
	lim := NewTokenBucket(5, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	runTokenBucketReserve(t, lim, request{t0, 2, t4, true})
	lim.SetBurstAt(t3, 4)
	runTokenBucketReserve(t, lim, request{t0, 4, t9, true}) // violates Limit and Burst
}

func TestTokenBucketReserveSetLimitCancel(t *testing.T) {
	lim := NewTokenBucket(5, 2)

	runTokenBucketReserve(t, lim, request{t0, 2, t0, true})
	r := runTokenBucketReserve(t, lim, request{t0, 2, t4, true})
	lim.SetLimitAt(t2, 10)
	r.CancelAt(t2) // 2 tokens back
	runTokenBucketReserve(t, lim, request{t2, 2, t3, true})
}

func TestTokenBucketReserveMax(t *testing.T) {
	lim := NewTokenBucket(10, 2)
	maxT := d

	runTokenBucketReserveMax(t, lim, request{t0, 2, t0, true}, maxT)
	runTokenBucketReserveMax(t, lim, request{t0, 1, t1, true}, maxT)  // reserve for close future
	runTokenBucketReserveMax(t, lim, request{t0, 1, t2, false}, maxT) // time to act too far in the future
}

func TestTokenBucketWaitSimple(t *testing.T) {
	lim := NewTokenBucket(10, 3)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runWait(t, lim, wait{"already-cancelled", ctx, 1, 0, false})

	runWait(t, lim, wait{"exceed-burst-error", context.Background(), 4, 0, false})

	runWait(t, lim, wait{"act-now", context.Background(), 2, 0, true})
	runWait(t, lim, wait{"act-later", context.Background(), 3, 2, true})
}

func TestTokenBucketWaitCancel(t *testing.T) {
	lim := NewTokenBucket(10, 3)

	ctx, cancel := context.WithCancel(context.Background())
	runWait(t, lim, wait{"act-now", ctx, 2, 0, true}) // after this lim.tokens = 1
	go func() {
		time.Sleep(d)
		cancel()
	}()
	runWait(t, lim, wait{"will-cancel", ctx, 3, 1, false})
	// should get 3 tokens back, and have lim.tokens = 2
	t.Logf("tokens:%v last:%v lastEvent:%v", lim.tokens, lim.last, lim.lastEvent)
	runWait(t, lim, wait{"act-now-after-cancel", context.Background(), 2, 0, true})
}

func TestTokenBucketWaitTimeout(t *testing.T) {
	lim := NewTokenBucket(10, 3)

	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	runWait(t, lim, wait{"act-now", ctx, 2, 0, true})
	runWait(t, lim, wait{"w-timeout-err", ctx, 3, 0, false})
}

func TestTokenBucketWaitInf(t *testing.T) {
	lim := NewTokenBucket(Inf, 0)

	runWait(t, lim, wait{"exceed-burst-no-error", context.Background(), 3, 0, true})
}

func runTokenBucketReserve(t *testing.T, lim *TokenBucket, req request) Reservation {
	return runTokenBucketReserveMax(t, lim, req, InfDuration)
}

func runTokenBucketReserveMax(t *testing.T, lim *TokenBucket, req request, maxReserve time.Duration) Reservation {
	r := lim.reserveN(req.t, req.n, maxReserve)
	if r.ok && (dSince(r.timeToAct) != dSince(req.act)) || r.ok != req.ok {
		t.Errorf("lim.reserveN(t%d, %v, %v) = (t%d, %v) want (t%d, %v)",
			dSince(req.t), req.n, maxReserve, dSince(r.timeToAct), r.ok, dSince(req.act), req.ok)
	}
	return &r
}

func BenchmarkTokenBucketAllowN(b *testing.B) {
	lim := NewTokenBucket(Every(1*time.Second), 1)
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lim.AllowN(now, 1)
		}
	})
}

func BenchmarkTokenBucketWaitNNoDelay(b *testing.B) {
	lim := NewTokenBucket(Limit(b.N), b.N)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lim.WaitN(ctx, 1)
	}
}
