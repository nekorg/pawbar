package cmd

import (
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/rs/zerolog"
)

// fakeSupervisor drives the reconcile loop without a compositor or kitty:
// listOutputs reports whatever the test plugged in, and startPanel records
// spawns instead of executing anything.
type fakeSupervisor struct {
	*supervisor
	connected []string
	listErr   error
	startErr  error
	spawned   []string
}

func newFake(t *testing.T, sel config.OutputSel, connected ...string) *fakeSupervisor {
	t.Helper()
	f := &fakeSupervisor{
		supervisor: newSupervisor(zerolog.New(io.Discard), sel, nil),
		connected:  connected,
	}
	f.listOutputs = func() ([]string, error) {
		if f.listErr != nil {
			return nil, f.listErr
		}
		return f.connected, nil
	}
	f.startPanel = func(name string) (*barPanel, error) {
		if f.startErr != nil {
			return nil, f.startErr
		}
		f.spawned = append(f.spawned, name)
		return &barPanel{startedAt: time.Now(), done: make(chan struct{})}, nil
	}
	return f
}

func (f *fakeSupervisor) runningNames() []string {
	names := make([]string, 0, len(f.running))
	for name := range f.running {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// crash simulates a panel dying on its own.
func (f *fakeSupervisor) crash(output string) {
	bp := f.running[output]
	if bp == nil {
		return
	}
	close(bp.done)
	f.handleExit(panelExit{output: output, bp: bp, err: errors.New("boom")})
}

func equal(a, b []string) bool { return slices.Equal(a, b) }

func TestReconcileOnePanelPerSelectedOutput(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1", "HDMI-A-1")
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"HDMI-A-1", "eDP-1"}) {
		t.Fatalf("running = %v, want a bar on each monitor", got)
	}

	// Reconciling again must not stack a second bar on either.
	f.reconcile()
	if len(f.spawned) != 2 {
		t.Errorf("spawned = %v, want one spawn per output", f.spawned)
	}
}

func TestReconcileHonoursSelection(t *testing.T) {
	f := newFake(t, config.OutputSel{Names: []string{"eDP-1"}}, "eDP-1", "HDMI-A-1")
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"eDP-1"}) {
		t.Fatalf("running = %v, want only the selected output", got)
	}

	// Widening the selection covers the other monitor without touching
	// the bar that was already up.
	f.sel = config.OutputSel{All: true}
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"HDMI-A-1", "eDP-1"}) {
		t.Fatalf("running = %v, want both", got)
	}
	if got := f.spawned; !equal(got, []string{"eDP-1", "HDMI-A-1"}) {
		t.Errorf("spawned = %v, want the first bar left alone", got)
	}

	// Narrowing it again stops the bar that lost its place.
	f.sel = config.OutputSel{Names: []string{"eDP-1"}}
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"eDP-1"}) {
		t.Fatalf("running = %v, want the deselected bar stopped", got)
	}
}

func TestUnplugAndReplug(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1", "HDMI-A-1")
	f.reconcile()

	f.connected = []string{"eDP-1"}
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"eDP-1"}) {
		t.Fatalf("running = %v, want the unplugged monitor's bar gone", got)
	}
	// The disconnected panel's exit must not be counted against it.
	if f.fails["HDMI-A-1"] != 0 {
		t.Errorf("fails = %d, want the unplug not to count as a failure", f.fails["HDMI-A-1"])
	}

	f.connected = []string{"eDP-1", "HDMI-A-1"}
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"HDMI-A-1", "eDP-1"}) {
		t.Fatalf("running = %v, want the bar back when the monitor returns", got)
	}
}

func TestStoppedPanelExitIsNotAFailure(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1", "HDMI-A-1")
	f.reconcile()
	bp := f.running["HDMI-A-1"]

	f.connected = []string{"eDP-1"}
	f.reconcile()
	// The exit event arrives after the supervisor already stopped it.
	f.handleExit(panelExit{output: "HDMI-A-1", bp: bp, err: nil})

	if f.fails["HDMI-A-1"] != 0 || len(f.retryAt) != 0 {
		t.Errorf("a supervisor-initiated stop was counted as a failure: fails=%v retryAt=%v", f.fails, f.retryAt)
	}
}

func TestCrashRespawnsAfterBackoff(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1")
	f.reconcile()
	f.crash("eDP-1")

	f.reconcile()
	if len(f.running) != 0 {
		t.Fatalf("respawned inside the backoff window")
	}
	f.retryAt["eDP-1"] = time.Now().Add(-time.Second)
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"eDP-1"}) {
		t.Fatalf("running = %v, want the bar back after the backoff", got)
	}
}

func TestCrashLoopGivesUp(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1")
	for range maxFails + 2 {
		f.reconcile()
		f.crash("eDP-1")
		f.retryAt["eDP-1"] = time.Now().Add(-time.Second)
	}
	if len(f.spawned) > maxFails {
		t.Fatalf("spawned %d times, want at most %d before giving up", len(f.spawned), maxFails)
	}

	// A monitor coming back is a fresh start.
	f.connected = nil
	f.reconcile()
	f.connected = []string{"eDP-1"}
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"eDP-1"}) {
		t.Fatalf("running = %v, want hotplug to reset the give-up state", got)
	}
}

func TestFallbackToUnpinnedBarWithoutMonitorList(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1")
	f.listErr = errors.New("no wayland")

	for range fallbackAfter - 1 {
		f.reconcile()
		if len(f.running) != 0 {
			t.Fatalf("fell back after a single failed query")
		}
	}
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{unpinned}) {
		t.Fatalf("running = %v, want one unpinned bar", got)
	}

	// Once monitors can be listed again, the pinned bars take over.
	f.listErr = nil
	f.reconcile()
	if got := f.runningNames(); !equal(got, []string{"eDP-1"}) {
		t.Fatalf("running = %v, want the pinned bar to replace the fallback", got)
	}
}

func TestStartFailureBacksOff(t *testing.T) {
	f := newFake(t, config.OutputSel{All: true}, "eDP-1")
	f.startErr = errors.New("kitty missing")
	f.reconcile()
	if len(f.running) != 0 {
		t.Fatalf("a failed start must not register a panel")
	}
	if f.fails["eDP-1"] != 1 || f.retryAt["eDP-1"].IsZero() {
		t.Errorf("failed start not backed off: fails=%v retryAt=%v", f.fails, f.retryAt)
	}
}

func TestSetEnvReplacesExisting(t *testing.T) {
	env := []string{"HOME=/home/x", "PAWBAR_OUTPUT=stale", "TERM=xterm"}
	got := setEnv(env, "PAWBAR_OUTPUT", "eDP-1")
	want := []string{"HOME=/home/x", "TERM=xterm", "PAWBAR_OUTPUT=eDP-1"}
	if !equal(got, want) {
		t.Errorf("setEnv = %v, want %v", got, want)
	}
	// The caller's slice must not be clobbered: katnip reuses it.
	if env[1] != "PAWBAR_OUTPUT=stale" {
		t.Errorf("setEnv mutated its input: %v", env)
	}
}

func TestOutputFlagSelection(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "<nil>"},
		{[]string{"eDP-1"}, "[eDP-1]"},
		{[]string{"eDP-1,HDMI-A-1"}, "[eDP-1, HDMI-A-1]"},
		{[]string{"eDP-1", "HDMI-A-1"}, "[eDP-1, HDMI-A-1]"},
		{[]string{"all"}, "all"},
		{[]string{"none"}, "none"},
	}
	for _, c := range cases {
		var o outputList
		for _, a := range c.args {
			if err := o.Set(a); err != nil {
				t.Fatal(err)
			}
		}
		got := "<nil>"
		if sel := o.sel(); sel != nil {
			got = sel.String()
		}
		if got != c.want {
			t.Errorf("--output %v = %s, want %s", c.args, got, c.want)
		}
	}
}
