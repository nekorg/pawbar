package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
)

// reconfMod is a Reconfigurer: hot reload swaps its options in place.
type reconfMod struct {
	reconfigured atomic.Int32
	failOnConfig atomic.Bool
}

func (m *reconfMod) Init(ctx *module.Ctx) error { return nil }
func (m *reconfMod) Render(w *module.Writer)    { w.Raw("rc") }

func (m *reconfMod) OnConfig(ctx *module.Ctx) error {
	m.reconfigured.Add(1)
	if m.failOnConfig.Load() {
		return errFail
	}
	return nil
}

var (
	errFail       = errTest("onconfig failed")
	reconfModInst *reconfMod
	registerRC    sync.Once
)

type errTest string

func (e errTest) Error() string { return string(e) }

func rcRegister() {
	registerRC.Do(func() {
		module.Register(module.Def{
			Name: "coretest-rc",
			New:  func() module.Module { reconfModInst = &reconfMod{}; return reconfModInst },
		})
	})
}

func compileBar(t *testing.T, src string) *config.Bar {
	t.Helper()
	f, issues := config.Load([]byte(src), "test.yaml")
	bar, ci := config.Compile(f)
	issues = append(issues, ci...)
	if len(issues) > 0 {
		t.Fatalf("config issues: %v", issues.Err())
	}
	return bar
}

func TestReloadKeepsUnchangedSlot(t *testing.T) {
	e, _ := startTestEngine(t, "right: [coretest]\n")
	waitUpdate(t, e, 2*time.Second)

	old := e.sides[Right][0]
	e.Reload(compileBar(t, "right: [coretest]\n"))
	if e.sides[Right][0] != old {
		t.Fatal("unchanged slot was not kept")
	}
}

func TestReloadRestartsChangedSlot(t *testing.T) {
	e, _ := startTestEngine(t, "right: [coretest]\n")
	waitUpdate(t, e, 2*time.Second)

	old := e.sides[Right][0]
	e.Reload(compileBar(t, "right:\n  - coretest:\n      on:\n        middle: { notify: hi }\n"))
	if e.sides[Right][0] == old {
		t.Fatal("changed slot (no Reconfigurer) was not restarted")
	}
	drainText(t, e, "t1") // fresh instance renders init-only state
}

func TestReloadReconfiguresInPlace(t *testing.T) {
	rcRegister()
	e, _ := startTestEngine(t, "right: [coretest-rc]\n")
	waitUpdate(t, e, 2*time.Second)

	old := e.sides[Right][0]
	inst := reconfModInst
	e.Reload(compileBar(t, "right:\n  - coretest-rc:\n      on:\n        middle: { notify: hi }\n"))
	if e.sides[Right][0] != old {
		t.Fatal("Reconfigurer slot was restarted instead of reconfigured")
	}
	waitFor(t, func() bool { return inst.reconfigured.Load() == 1 }, "OnConfig never ran")
}

func TestReloadSlotCountChange(t *testing.T) {
	rcRegister()
	e, _ := startTestEngine(t, "right: [coretest, coretest-rc]\n")
	waitUpdate(t, e, 2*time.Second)

	e.Reload(compileBar(t, "right: [coretest]\n"))
	if len(e.sides[Right]) != 1 {
		t.Fatalf("expected 1 right slot, got %d", len(e.sides[Right]))
	}
	// The removed runner's late publishes must be dropped, not delivered.
	e.Reload(compileBar(t, "right: [coretest, coretest-rc]\n"))
	if len(e.sides[Right]) != 2 {
		t.Fatalf("expected 2 right slots, got %d", len(e.sides[Right]))
	}
}

func TestReloadFailedOnConfigRequestsRestart(t *testing.T) {
	rcRegister()
	e, _ := startTestEngine(t, "right: [coretest-rc]\n")
	waitUpdate(t, e, 2*time.Second)

	old := e.sides[Right][0]
	reconfModInst.failOnConfig.Store(true)
	e.Reload(compileBar(t, "right:\n  - coretest-rc:\n      on:\n        middle: { notify: hi }\n"))

	select {
	case s := <-e.Restarts():
		e.RestartSlot(s)
	case <-time.After(2 * time.Second):
		t.Fatal("failed OnConfig did not request a restart")
	}
	if e.sides[Right][0] == old {
		t.Fatal("RestartSlot did not replace the runner")
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestSnapshotsReseedAfterReload(t *testing.T) {
	e, bar := startTestEngine(t, "right: [coretest]\n")
	waitUpdate(t, e, 2*time.Second)

	// Reload with an identical bar: the runner is kept, pushes nothing.
	e.Reload(bar)

	seen := 0
	e.Snapshots(func(side Side, idx int, levels [][]module.Segment) {
		seen++
		if side != Right || idx != 0 || len(levels) == 0 || len(levels[0]) == 0 {
			t.Fatalf("bad snapshot: side=%v idx=%d levels=%v", side, idx, levels)
		}
	})
	if seen != 1 {
		t.Fatalf("kept runner's snapshot must survive reload, got %d snapshots", seen)
	}
}
