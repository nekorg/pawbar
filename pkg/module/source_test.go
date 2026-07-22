package module

import (
	"testing"
	"time"
)

func TestChanSourceDeliversAndStops(t *testing.T) {
	ch := make(chan int, 4)
	src := Chan(ch)

	got := make(chan any, 4)
	conn, err := src.open(func(v int) { got <- v })
	if err != nil {
		t.Fatal(err)
	}
	ch <- 7
	select {
	case v := <-got:
		if v.(int) != 7 {
			t.Errorf("got %v", v)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}

	conn.Stop()
	conn.Stop() // double-stop must not panic
	ch <- 8
	select {
	case v := <-got:
		t.Errorf("delivery after stop: %v", v)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTickerEmits(t *testing.T) {
	tk := NewTicker(10 * time.Millisecond)
	got := make(chan time.Time, 8)
	conn, err := tk.Source().open(func(v time.Time) { got <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Stop()

	for range 2 {
		select {
		case <-got:
		case <-time.After(time.Second):
			t.Fatal("tick did not fire")
		}
	}
}

func TestTickerSingleSubscription(t *testing.T) {
	tk := NewTicker(time.Hour)
	conn, err := tk.Source().open(func(time.Time) {})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Stop()
	if _, err := tk.Source().open(func(time.Time) {}); err == nil {
		t.Fatal("second subscription should fail")
	}
}

func TestTickerSetReschedules(t *testing.T) {
	tk := NewTicker(time.Hour)
	got := make(chan time.Time, 8)
	conn, err := tk.Source().open(func(v time.Time) { got <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Stop()

	select {
	case <-got:
		t.Fatal("hour ticker should not fire immediately")
	case <-time.After(30 * time.Millisecond):
	}

	tk.Set(10 * time.Millisecond)
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("Set did not reschedule the pending tick")
	}
}

func TestAlignedTickerBoundary(t *testing.T) {
	interval := 250 * time.Millisecond
	tk := NewAlignedTicker(interval)
	got := make(chan time.Time, 8)
	conn, err := tk.Source().open(func(v time.Time) { got <- v })
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Stop()

	select {
	case ts := <-got:
		off := ts.Sub(ts.Truncate(interval))
		if off > 100*time.Millisecond {
			t.Errorf("tick %v off boundary by %v", ts, off)
		}
	case <-time.After(time.Second):
		t.Fatal("aligned tick did not fire")
	}
}

func TestOnSubscriptionPlumbing(t *testing.T) {
	host := &fakeHost{}
	ctx := NewCtx(host, nil)

	ch := make(chan string, 1)
	var delivered []string
	sub := On(ctx, Chan(ch), func(s string) { delivered = append(delivered, s) })

	if len(host.subs) != 1 || host.subs[0] != sub {
		t.Fatal("On must hand the subscription to the host")
	}

	emitted := make(chan any, 1)
	if err := sub.Open(func(v any) { emitted <- v }); err != nil {
		t.Fatal(err)
	}
	ch <- "hi"
	select {
	case v := <-emitted:
		sub.Deliver(v)
	case <-time.After(time.Second):
		t.Fatal("no emit")
	}
	if len(delivered) != 1 || delivered[0] != "hi" {
		t.Errorf("delivered %v", delivered)
	}
	sub.Close()
	sub.Close() // idempotent
}

type fakeHost struct {
	subs   []*Subscription
	states []string
}

func (h *fakeHost) Name() string                      { return "fake" }
func (h *fakeHost) Logf(string, ...any)               {}
func (h *fakeHost) SetState(string, bool)             {}
func (h *fakeHost) States() []string                  { return h.states }
func (h *fakeHost) Block() Block                      { return Block{} }
func (h *fakeHost) Refresh()                          {}
func (h *fakeHost) Go(fn func())                      { fn() }
func (h *fakeHost) SubscriptionAdded(s *Subscription) { h.subs = append(h.subs, s) }
