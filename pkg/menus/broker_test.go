package menus

import (
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/codelif/outputs"
	"github.com/nekorg/katnip"
)

// fakeSpawner stands in for kitty: a real process with a real katnip stream,
// so lifecycle (reap, kill, unmap) is exercised, without a compositor.
type fakeSpawner struct {
	mu      sync.Mutex
	spawned map[string]int
	panels  []*katnip.Panel
}

func newFakeSpawner() *fakeSpawner {
	return &fakeSpawner{spawned: make(map[string]int)}
}

func (f *fakeSpawner) spawn(mon outputs.Monitor) (*katnip.Panel, error) {
	p := katnip.NewPanel("pawpanel-test", katnip.Config{})
	p.Cmd = exec.Command("sleep", "30")
	if err := p.Start(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.spawned[mon.Name]++
	f.panels = append(f.panels, p)
	f.mu.Unlock()
	return p, nil
}

func (f *fakeSpawner) count(output string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawned[output]
}

var testMonitors = []outputs.Monitor{
	{Name: "DP-1", ScaledWidth: 2560, ScaledHeight: 1440, Scale: 1, IsPrimary: true},
	{Name: "DP-2", ScaledWidth: 1920, ScaledHeight: 1080, Scale: 1},
}

func testBroker(t *testing.T) (*Broker, *fakeSpawner) {
	t.Helper()
	f := newFakeSpawner()
	b := NewBroker()
	b.spawn = f.spawn
	b.ready = func(*Spare) bool { return true }
	b.monitors = func() ([]outputs.Monitor, error) { return testMonitors, nil }
	t.Cleanup(b.Close)
	return b, f
}

func (b *Broker) idleCount(output string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.idle[output])
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The pool is desktop-wide: two spares, on one output, however many bars ask.
func TestBrokerKeepsTwoSparesOnOneOutput(t *testing.T) {
	b, _ := testBroker(t)
	b.WarmFor("DP-1")

	waitFor(t, "two spares on DP-1", func() bool { return b.idleCount("DP-1") == poolTarget })
	if n := b.idleCount("DP-2"); n != 0 {
		t.Fatalf("DP-2 holds %d spares; the pointer is not there", n)
	}
}

func TestBrokerMovesSparesWithThePointer(t *testing.T) {
	b, f := testBroker(t)
	b.WarmFor("DP-1")
	waitFor(t, "spares on DP-1", func() bool { return b.idleCount("DP-1") == poolTarget })

	b.WarmFor("DP-2")
	waitFor(t, "spares on DP-2", func() bool { return b.idleCount("DP-2") == poolTarget })
	if n := b.idleCount("DP-1"); n != 0 {
		t.Fatalf("DP-1 kept %d spares after the pointer left; they can only open there", n)
	}
	if got := f.count("DP-1"); got != poolTarget {
		t.Fatalf("DP-1 spawned %d spares, want %d", got, poolTarget)
	}
}

func TestAcquireTakesAWarmSpare(t *testing.T) {
	b, f := testBroker(t)
	b.WarmFor("DP-1")
	waitFor(t, "spares on DP-1", func() bool { return b.idleCount("DP-1") == poolTarget })

	id, path, err := b.Acquire("DP-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if id == 0 || path == "" {
		t.Fatalf("Acquire = %d, %q; want a lease and a wire to attach", id, path)
	}
	// The pool refills behind the lease.
	waitFor(t, "the pool to refill", func() bool { return b.idleCount("DP-1") == poolTarget })
	if got, want := f.count("DP-1"), poolTarget+1; got != want {
		t.Fatalf("spawned %d panels on DP-1, want %d", got, want)
	}
}

func TestAcquireSpawnsWhenThePoolIsElsewhere(t *testing.T) {
	b, f := testBroker(t)
	b.WarmFor("DP-1")
	waitFor(t, "spares on DP-1", func() bool { return b.idleCount("DP-1") == poolTarget })

	if _, _, err := b.Acquire("DP-2"); err != nil {
		t.Fatalf("Acquire on the cold output: %v", err)
	}
	if got := f.count("DP-2"); got != 1 {
		t.Fatalf("DP-2 spawned %d panels, want 1 (the miss)", got)
	}
}

func TestReleaseReapsThePanel(t *testing.T) {
	b, _ := testBroker(t)
	id, _, err := b.Acquire("DP-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	b.mu.Lock()
	sp := b.leased[id]
	b.mu.Unlock()
	if sp == nil {
		t.Fatal("the lease is not recorded")
	}

	b.Release(id)
	select {
	case <-sp.done:
	default:
		t.Fatal("Release returned before the panel process was reaped")
	}
	b.mu.Lock()
	still := b.leased[id]
	b.mu.Unlock()
	if still != nil {
		t.Fatal("the lease outlived its panel")
	}
}

// A spare latches its output's scale while warming, so one warmed before a
// mode change must not be handed out afterwards.
func TestAcquireDiscardsSparesWarmedAtTheOldMode(t *testing.T) {
	b, f := testBroker(t)
	b.WarmFor("DP-1")
	waitFor(t, "spares on DP-1", func() bool { return b.idleCount("DP-1") == poolTarget })

	resized := []outputs.Monitor{{Name: "DP-1", ScaledWidth: 3840, ScaledHeight: 2160, Scale: 2, IsPrimary: true}}
	b.monitors = func() ([]outputs.Monitor, error) { return resized, nil }

	before := f.count("DP-1")
	if _, _, err := b.Acquire("DP-1"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := f.count("DP-1"); got <= before {
		t.Fatal("a spare warmed at the old mode was handed out instead of a fresh one")
	}
}

func TestDropReclaimsAnOutputsSpares(t *testing.T) {
	b, _ := testBroker(t)
	b.WarmFor("DP-1")
	waitFor(t, "spares on DP-1", func() bool { return b.idleCount("DP-1") == poolTarget })

	b.Drop("DP-1")
	if n := b.idleCount("DP-1"); n != 0 {
		t.Fatalf("DP-1 kept %d spares after its monitor went away", n)
	}
}
