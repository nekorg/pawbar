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

func (f *fakeSpawner) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.spawned {
		n += c
	}
	return n
}

var testMonitors = []outputs.Monitor{
	{Name: "DP-1", ScaledWidth: 2560, ScaledHeight: 1440, Scale: 1, IsPrimary: true},
	{Name: "DP-2", ScaledWidth: 1920, ScaledHeight: 1080, Scale: 1},
}

// testBroker returns a broker with both test monitors carrying a bar.
func testBroker(t *testing.T, pool PoolSettings) (*Broker, *fakeSpawner) {
	t.Helper()
	f := newFakeSpawner()
	b := NewBroker(pool)
	b.spawn = f.spawn
	b.ready = func(*Spare) bool { return true }
	b.monitors = func() ([]outputs.Monitor, error) { return testMonitors, nil }
	t.Cleanup(b.Close)
	b.SetOutputs([]string{"DP-1", "DP-2"})
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

// settled waits for the pool to reach a split and then holds it there, so a
// test can tell "arrived" apart from "passed through".
func settled(t *testing.T, b *Broker, one, two int) {
	t.Helper()
	waitFor(t, "the pool to settle", func() bool {
		return b.idleCount("DP-1") == one && b.idleCount("DP-2") == two
	})
	time.Sleep(2 * settleDelay)
	if got, want := [2]int{b.idleCount("DP-1"), b.idleCount("DP-2")}, [2]int{one, two}; got != want {
		t.Fatalf("pool moved on to %v, want it to stay at %v", got, want)
	}
}

// A budget too small to cover the desktop drains onto the monitor the
// pointer is on, which is the only one that can be clicked next.
func TestScarcePoolFollowsTheIntent(t *testing.T) {
	b, _ := testBroker(t, PoolSettings{Target: 2, Max: 2})
	b.WarmFor("DP-1")
	settled(t, b, 2, 0)

	b.WarmFor("DP-2")
	settled(t, b, 0, 2)
}

// The default budget keeps the intent monitor's whole chain warm and hedges
// each of the others with one spare.
func TestAutoPoolHedgesTheOtherMonitors(t *testing.T) {
	b, f := testBroker(t, PoolSettings{Target: 2, Max: -1})
	b.WarmFor("DP-1")
	settled(t, b, 2, 1)

	before := f.total()
	b.WarmFor("DP-2")
	settled(t, b, 1, 2)
	if got := f.total() - before; got != 1 {
		t.Fatalf("crossing the screen edge spawned %d panels, want 1", got)
	}
}

// With room for every monitor's chain, the pointer moving costs nothing.
func TestAmplePoolIgnoresThePointer(t *testing.T) {
	b, f := testBroker(t, PoolSettings{Target: 2, Max: 4})
	b.WarmFor("DP-1")
	settled(t, b, 2, 2)

	before := f.total()
	b.WarmFor("DP-2")
	settled(t, b, 2, 2)
	if got := f.total(); got != before {
		t.Fatalf("spawned %d more panels for a pointer move, want none", got-before)
	}
}

// Pooling off still opens menus, it just pays for each one.
func TestPoolingOffKeepsNoSpares(t *testing.T) {
	b, f := testBroker(t, PoolSettings{Target: 2, Max: 0})
	b.WarmFor("DP-1")
	time.Sleep(3 * settleDelay)
	if n := f.total(); n != 0 {
		t.Fatalf("spawned %d spares with pooling off", n)
	}

	if _, _, err := b.Acquire("DP-1"); err != nil {
		t.Fatalf("Acquire with pooling off: %v", err)
	}
	if n := f.count("DP-1"); n != 1 {
		t.Fatalf("DP-1 spawned %d panels, want 1 (the cold start)", n)
	}
}

func TestAcquireTakesAWarmSpare(t *testing.T) {
	b, f := testBroker(t, PoolSettings{Target: 2, Max: 2})
	b.WarmFor("DP-1")
	settled(t, b, 2, 0)

	id, path, err := b.Acquire("DP-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if id == 0 || path == "" {
		t.Fatalf("Acquire = %d, %q; want a lease and a wire to attach", id, path)
	}
	// The pool refills behind the lease.
	waitFor(t, "the pool to refill", func() bool { return b.idleCount("DP-1") == 2 })
	if got, want := f.count("DP-1"), 3; got != want {
		t.Fatalf("spawned %d panels on DP-1, want %d", got, want)
	}
}

func TestAcquireSpawnsWhenThePoolIsElsewhere(t *testing.T) {
	b, f := testBroker(t, PoolSettings{Target: 2, Max: 2})
	b.WarmFor("DP-1")
	settled(t, b, 2, 0)

	if _, _, err := b.Acquire("DP-2"); err != nil {
		t.Fatalf("Acquire on the cold output: %v", err)
	}
	if got := f.count("DP-2"); got != 1 {
		t.Fatalf("DP-2 spawned %d panels, want 1 (the miss)", got)
	}
}

// Opening a menu is intent too, so the pool follows the click.
func TestAcquireMovesTheIntent(t *testing.T) {
	b, _ := testBroker(t, PoolSettings{Target: 2, Max: 2})
	b.WarmFor("DP-1")
	settled(t, b, 2, 0)

	if _, _, err := b.Acquire("DP-2"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	settled(t, b, 0, 2)
}

func TestReleaseReapsThePanel(t *testing.T) {
	b, _ := testBroker(t, PoolSettings{Target: 2, Max: 2})
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
	b, f := testBroker(t, PoolSettings{Target: 2, Max: 2})
	b.WarmFor("DP-1")
	settled(t, b, 2, 0)

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

func TestSetOutputsReclaimsAnOutputsSpares(t *testing.T) {
	b, _ := testBroker(t, PoolSettings{Target: 2, Max: 2})
	b.WarmFor("DP-1")
	settled(t, b, 2, 0)

	b.SetOutputs([]string{"DP-2"})
	waitFor(t, "DP-1 to give its spares up", func() bool { return b.idleCount("DP-1") == 0 })
	settled(t, b, 0, 2)
}
