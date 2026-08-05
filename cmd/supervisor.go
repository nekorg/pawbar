// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"context"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/codelif/outputs"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/core"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/monitor"
	"github.com/rs/zerolog"
)

const (
	// pollInterval is how often the supervisor re-reads the connected
	// outputs. Monitor hotplug is the only thing it can miss, and a couple
	// of seconds to notice a plugged-in screen is imperceptible.
	pollInterval = 2 * time.Second
	// respawnBase/respawnMax bound the backoff after a panel dies.
	respawnBase = time.Second
	respawnMax  = 30 * time.Second
	// steadyRun is how long a panel must survive before its failure count
	// resets: anything shorter is a crash loop, not a working bar.
	steadyRun = time.Minute
	// maxFails stops a hopeless respawn loop (a compositor that refuses
	// the layer surface, a broken kitty). Hotplug or a config change gives
	// that output another chance.
	maxFails = 10
	// shutdownGrace is how long children get to exit on their own after
	// SIGINT before they are killed.
	shutdownGrace = 3 * time.Second
	// fallbackAfter is how many consecutive failed monitor queries it
	// takes before the supervisor gives up on pinning and starts one
	// compositor-placed bar.
	fallbackAfter = 3
	// unpinned is the output key of that fallback bar.
	unpinned = ""
)

// label names an output in logs, including the unpinned fallback bar.
func label(output string) string {
	if output == unpinned {
		return "unpinned"
	}
	return output
}

// barPanel is one running kitty panel and the output it was pinned to.
type barPanel struct {
	panel     *katnip.Panel
	startedAt time.Time
	// done closes once the panel process has been reaped.
	done chan struct{}
	// stopped marks a panel the supervisor itself asked to exit, so its
	// exit is not counted as a failure.
	stopped bool
}

// panelExit identifies the panel that exited, not just its output: a
// replacement may already be running by the time the event is handled.
type panelExit struct {
	output string
	bp     *barPanel
	err    error
}

// supervisor owns one kitty panel per selected output: it spawns them
// pinned to their monitor, restarts them when they die, and follows
// monitor hotplug and config changes.
type supervisor struct {
	log zerolog.Logger
	// flagSel is the --output selection; when set it wins over the config
	// on every reload.
	flagSel *config.OutputSel
	sel     config.OutputSel

	running map[string]*barPanel
	fails   map[string]int
	retryAt map[string]time.Time
	// queryFails counts consecutive failed monitor queries, which is how
	// the supervisor decides to fall back to one unpinned panel.
	queryFails int

	exits chan panelExit

	// Seams for tests, which have neither a compositor nor a kitty.
	listOutputs func() ([]string, error)
	startPanel  func(name string) (*barPanel, error)
}

func newSupervisor(log zerolog.Logger, sel config.OutputSel, flagSel *config.OutputSel) *supervisor {
	s := &supervisor{
		log:     log,
		flagSel: flagSel,
		sel:     sel,
		running: make(map[string]*barPanel),
		fails:   make(map[string]int),
		retryAt: make(map[string]time.Time),
		exits:   make(chan panelExit, 4),
	}
	s.listOutputs = connectedOutputs
	s.startPanel = s.startKitty
	return s
}

// run reconciles panels against the connected outputs until a signal
// arrives. It never exits on its own: a bar whose monitor was unplugged
// comes back when the monitor does.
func (s *supervisor) run() int {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadCh, err := core.WatchConfig(ctx.Done(), configPath(), s.log)
	if err != nil {
		s.log.Warn().Msgf("config: hot reload of the output selection disabled: %v", err)
	}

	tick := time.NewTicker(pollInterval)
	defer tick.Stop()

	s.reconcile()

	for {
		select {
		case <-tick.C:
			s.reconcile()

		case e := <-s.exits:
			s.handleExit(e)
			s.reconcile()

		case <-reloadCh:
			if s.reloadSelection() {
				s.reconcile()
			}

		case sig := <-sigs:
			s.log.Info().Msgf("received %s, stopping %d panel(s)", canonicalSignalName(sig), len(s.running))
			s.shutdown()
			return 0
		}
	}
}

// reloadSelection re-reads bar.outputs after a config change. Panels that
// stay selected are left alone; each one hot-reloads its own section.
func (s *supervisor) reloadSelection() bool {
	if s.flagSel != nil {
		return false // --output pins the selection for this run
	}
	f, issues := config.Read(configPath())
	if issues.Fatal() {
		s.log.Warn().Msg("config: unreadable, keeping the current output selection")
		return false
	}
	if f.Bar.Outputs.String() == s.sel.String() {
		return false
	}
	s.log.Info().Msgf("config: output selection is now %s", f.Bar.Outputs)
	s.sel = f.Bar.Outputs
	// A newly selected output deserves a clean slate.
	clear(s.fails)
	clear(s.retryAt)
	return true
}

// reconcile brings the running panels in line with the connected outputs
// and the current selection.
func (s *supervisor) reconcile() {
	connected, err := s.listOutputs()
	if err != nil {
		s.queryFails++
		s.log.Debug().Msgf("monitors: query failed (%v); keeping the current panels", err)
		// Without a monitor list there is nothing to pin a bar to. Rather
		// than leave the user with no bar at all, fall back to a single
		// unpinned panel and let the compositor place it, as pawbar did
		// before it knew about monitors.
		if s.queryFails >= fallbackAfter && len(s.running) == 0 {
			s.log.Warn().Msgf("monitors: unavailable (%v); starting one unpinned bar", err)
			s.spawn(unpinned)
		}
		return
	}
	s.queryFails = 0

	// A working monitor list supersedes the fallback bar.
	if bp := s.running[unpinned]; bp != nil {
		s.log.Info().Msg("monitors: available again, restarting the bar pinned to its output")
		s.stop(unpinned, bp)
	}

	// A monitor that is not there has no history worth keeping: whatever
	// went wrong with its bar, a returning monitor gets a clean slate.
	for name := range s.fails {
		if !slices.Contains(connected, name) {
			delete(s.fails, name)
			delete(s.retryAt, name)
		}
	}

	for name, bp := range s.running {
		if slices.Contains(connected, name) && s.sel.Matches(name) {
			continue
		}
		if !slices.Contains(connected, name) {
			s.log.Info().Msgf("%s: monitor disconnected, waiting for it to come back", name)
		} else {
			s.log.Info().Msgf("%s: no longer selected, stopping its bar", name)
		}
		s.stop(name, bp)
	}

	now := time.Now()
	for _, name := range connected {
		if !s.sel.Matches(name) || s.running[name] != nil {
			continue
		}
		if s.fails[name] >= maxFails {
			continue
		}
		if at, ok := s.retryAt[name]; ok && now.Before(at) {
			continue
		}
		s.spawn(name)
	}
}

// spawn starts the bar for one output.
func (s *supervisor) spawn(name string) {
	bp, err := s.startPanel(name)
	if err != nil {
		s.log.Error().Msgf("%s: cannot start panel: %v", label(name), err)
		s.failed(name)
		return
	}
	s.log.Info().Msgf("%s: bar started", label(name))
	s.running[name] = bp
}

// startKitty launches the panel, pinned to its output both in kitty
// (--output-name) and in the panel process's environment, and wires up its
// log stream and reaper.
func (s *supervisor) startKitty(name string) (*barPanel, error) {
	p := katnip.NewPanel("pawbar", barPanelConfig(name))
	p.Cmd.Env = setEnv(p.Cmd.Env, monitor.EnvOutput, name)

	if err := p.Start(); err != nil {
		return nil, err
	}
	bp := &barPanel{panel: p, startedAt: time.Now(), done: make(chan struct{})}

	logging.Go("supervisor.pump."+name, func() { pump(p.Reader()) })
	go func() {
		err := p.Wait()
		close(bp.done)
		s.exits <- panelExit{output: name, bp: bp, err: err}
	}()
	return bp, nil
}

// stop asks a panel to exit and forgets it; its exit event is recognised
// as ours by identity and ignored.
func (s *supervisor) stop(name string, bp *barPanel) {
	bp.stopped = true
	delete(s.running, name)
	if bp.panel == nil {
		return
	}
	if err := bp.panel.Stop(); err != nil {
		s.log.Debug().Msgf("%s: stopping panel: %v", label(name), err)
	}
}

func (s *supervisor) handleExit(e panelExit) {
	if e.bp.stopped {
		return // the supervisor asked for this one
	}
	if s.running[e.output] == e.bp {
		delete(s.running, e.output)
	}

	// A panel that ran for a while and then died is not a crash loop.
	if time.Since(e.bp.startedAt) > steadyRun {
		delete(s.fails, e.output)
	}

	if e.err == nil {
		s.log.Info().Msgf("%s: bar exited", label(e.output))
	} else {
		s.log.Warn().Msgf("%s: bar exited: %v", label(e.output), e.err)
	}
	s.failed(e.output)
}

// failed records a failed panel and schedules the next attempt.
func (s *supervisor) failed(name string) {
	s.fails[name]++
	if s.fails[name] >= maxFails {
		s.log.Error().Msgf("%s: giving up after %d failed starts; plug the monitor back in or edit the config to retry",
			label(name), s.fails[name])
		return
	}
	backoff := min(respawnBase<<(s.fails[name]-1), respawnMax)
	s.retryAt[name] = time.Now().Add(backoff)
	s.log.Debug().Msgf("%s: retrying in %v", label(name), backoff)
}

// shutdown asks every panel to exit, then kills whatever is left. kitty is
// started with Pdeathsig, so worst case a panel dies with the supervisor.
func (s *supervisor) shutdown() {
	panels := make([]*barPanel, 0, len(s.running))
	for name, bp := range s.running {
		s.stop(name, bp)
		panels = append(panels, bp)
	}
	deadline := time.After(shutdownGrace)
	for _, bp := range panels {
		select {
		case <-bp.done:
		case <-deadline:
			s.log.Warn().Msg("panels did not exit in time, killing them")
			for _, bp := range panels {
				if bp.panel != nil {
					_ = bp.panel.Kill()
				}
			}
			return
		}
	}
}

// pump forwards a panel's log stream to stdout. Each panel tags its own
// lines with its output, so several bars stay tellable apart here and in
// the shared log file.
//
// The stream never ends (it is a shared-memory ring the panel process
// writes into), so this goroutine outlives the panel it was started for.
// That costs one katnip stream buffer per spawn, which is why respawns are
// backed off and capped.
func pump(r io.Reader) {
	if r == nil {
		return
	}
	_, _ = io.Copy(os.Stdout, r)
}

// connectedOutputs returns the names of the connected monitors, ordered
// left to right so bars come up in a predictable order.
func connectedOutputs() ([]string, error) {
	monitors, err := outputs.GetMonitors()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(monitors, func(a, b outputs.Monitor) int {
		if a.X != b.X {
			return a.X - b.X
		}
		return strings.Compare(a.Name, b.Name)
	})
	names := make([]string, 0, len(monitors))
	for _, m := range monitors {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// setEnv replaces key in a process environment. Appending alone would not
// do: with a duplicate key the child's getenv keeps the first one, and the
// supervisor's own environment may already carry PAWBAR_OUTPUT.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return append(out, prefix+value)
}
