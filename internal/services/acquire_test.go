// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package services

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fake struct {
	stops atomic.Int32
	err   error
}

func (f *fake) Stop() error {
	f.stops.Add(1)
	return f.err
}

// shortLinger shrinks the linger window so a test does not wait five
// seconds to watch a service stop, and restores it afterwards.
func shortLinger(t *testing.T, d time.Duration) {
	t.Helper()
	prev := lingerDelay
	lingerDelay = d
	t.Cleanup(func() { lingerDelay = prev })
}

// reset clears the registry so one test's services cannot leak into the
// next. The registry is package-global, so these tests cannot run parallel.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		acqMu.Lock()
		clear(acqReg)
		acqMu.Unlock()
	})
}

func TestAcquireStartsOnceAndSharesTheInstance(t *testing.T) {
	reset(t)
	var starts atomic.Int32
	f := &fake{}
	factory := func() (*fake, error) {
		starts.Add(1)
		return f, nil
	}

	a, relA, err := Acquire("svc", factory)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	b, relB, err := Acquire("svc", factory)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if a != b {
		t.Errorf("acquires returned different instances: %p, %p", a, b)
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("factory ran %d times, want 1", got)
	}
	relA()
	relB()
}

func TestServiceStopsAfterTheLastRelease(t *testing.T) {
	reset(t)
	shortLinger(t, 10*time.Millisecond)
	f := &fake{}

	_, relA, _ := Acquire("svc", func() (*fake, error) { return f, nil })
	_, relB, _ := Acquire("svc", func() (*fake, error) { return f, nil })

	relA()
	time.Sleep(30 * time.Millisecond)
	if got := f.stops.Load(); got != 0 {
		t.Fatalf("stopped with a holder still live (stops=%d)", got)
	}

	relB()
	time.Sleep(50 * time.Millisecond)
	if got := f.stops.Load(); got != 1 {
		t.Errorf("stops = %d, want 1", got)
	}
}

func TestReAcquireInsideLingerKeepsTheService(t *testing.T) {
	reset(t)
	shortLinger(t, 100*time.Millisecond)
	var starts atomic.Int32
	f := &fake{}
	factory := func() (*fake, error) {
		starts.Add(1)
		return f, nil
	}

	_, rel, _ := Acquire("svc", factory)
	rel()
	// Inside the linger window: this is the hot-reload case the delay exists for.
	time.Sleep(20 * time.Millisecond)
	_, rel2, err := Acquire("svc", factory)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if got := f.stops.Load(); got != 0 {
		t.Errorf("service bounced across the linger window (stops=%d)", got)
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("factory ran %d times, want 1", got)
	}
	rel2()
}

func TestReleaseIsIdempotent(t *testing.T) {
	reset(t)
	shortLinger(t, 10*time.Millisecond)
	f := &fake{}

	_, relA, _ := Acquire("svc", func() (*fake, error) { return f, nil })
	_, relB, _ := Acquire("svc", func() (*fake, error) { return f, nil })

	relA()
	relA()
	relA()
	// Two holders, one of them released three times: the service must live.
	time.Sleep(40 * time.Millisecond)
	if got := f.stops.Load(); got != 0 {
		t.Fatalf("a repeated release stopped a held service (stops=%d)", got)
	}

	relB()
	time.Sleep(50 * time.Millisecond)
	if got := f.stops.Load(); got != 1 {
		t.Errorf("stops = %d, want 1", got)
	}
}

func TestFactoryErrorIsNotCached(t *testing.T) {
	reset(t)
	boom := errors.New("boom")
	var starts atomic.Int32

	_, _, err := Acquire("svc", func() (*fake, error) {
		starts.Add(1)
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}

	f := &fake{}
	svc, rel, err := Acquire("svc", func() (*fake, error) {
		starts.Add(1)
		return f, nil
	})
	if err != nil {
		t.Fatalf("retry after a failed start: %v", err)
	}
	if svc != f {
		t.Error("retry did not get the newly started service")
	}
	if got := starts.Load(); got != 2 {
		t.Errorf("factory ran %d times, want 2 (the failure must not be cached)", got)
	}
	rel()
}

func TestAcquireRejectsAMismatchedType(t *testing.T) {
	reset(t)
	shortLinger(t, 10*time.Millisecond)
	f := &fake{}

	_, rel, err := Acquire("svc", func() (*fake, error) { return f, nil })
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Same name, different type: an error, not a panic.
	_, _, err = Acquire("svc", func() (*int, error) { return new(int), nil })
	if err == nil {
		t.Fatal("acquiring under a mismatched type succeeded, want an error")
	}

	// And the failed acquire must not have left a ref behind.
	rel()
	time.Sleep(50 * time.Millisecond)
	if got := f.stops.Load(); got != 1 {
		t.Errorf("stops = %d, want 1 (a rejected acquire leaked a ref)", got)
	}
}

func TestReleaseAgainstAStoppedInstanceIsANoop(t *testing.T) {
	reset(t)
	shortLinger(t, 10*time.Millisecond)

	first := &fake{}
	_, relFirst, _ := Acquire("svc", func() (*fake, error) { return first, nil })

	// Let the first instance go all the way down.
	relFirst()
	time.Sleep(50 * time.Millisecond)
	if got := first.stops.Load(); got != 1 {
		t.Fatalf("first instance did not stop (stops=%d)", got)
	}

	second := &fake{}
	_, relSecond, _ := Acquire("svc", func() (*fake, error) { return second, nil })

	// The release closure holds the entry it was issued against, not the
	// name, so a late call cannot touch the instance that replaced it.
	relFirst()
	time.Sleep(50 * time.Millisecond)
	if got := second.stops.Load(); got != 0 {
		t.Errorf("a stale release stopped the live instance (stops=%d)", got)
	}
	relSecond()
}

func TestASlowFactoryDoesNotBlockOtherServices(t *testing.T) {
	reset(t)
	slow := make(chan struct{})
	started := make(chan struct{})

	go func() {
		_, rel, err := Acquire("slow", func() (*fake, error) {
			close(started)
			<-slow
			return &fake{}, nil
		})
		if err == nil {
			rel()
		}
	}()
	<-started

	// "slow" is mid-factory. Acquiring an unrelated service must not wait
	// on it: the registry lock is not held across a factory call.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, rel, err := Acquire("quick", func() (*fake, error) { return &fake{}, nil })
		if err != nil {
			t.Errorf("quick acquire: %v", err)
			return
		}
		rel()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("an unrelated acquire blocked behind a slow factory")
	}
	close(slow)
}

func TestConcurrentAcquiresStartOneService(t *testing.T) {
	reset(t)
	var starts atomic.Int32
	f := &fake{}

	const n = 16
	var wg sync.WaitGroup
	rels := make([]func(), n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, rel, err := Acquire("svc", func() (*fake, error) {
				starts.Add(1)
				// Wide enough that every racer is inside Acquire.
				time.Sleep(20 * time.Millisecond)
				return f, nil
			})
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			rels[i] = rel
		}()
	}
	wg.Wait()

	if got := starts.Load(); got != 1 {
		t.Errorf("factory ran %d times under %d concurrent acquires, want 1", got, n)
	}
	for _, rel := range rels {
		if rel != nil {
			rel()
		}
	}
}
