// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/codelif/outputs"
	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

// settleDelay coalesces plan changes. At the default budget crossing a
// screen edge moves a spare, and a pointer dragged along the edge would
// otherwise move one per event. Nothing here is latency critical: a menu
// that misses the pool spawns on its own path.
const settleDelay = 150 * time.Millisecond

// readyTimeout bounds waiting for a freshly spawned spare to warm up
// (kitty spawn + vaxis handshake + off-screen map). Generous; the wait
// returns as soon as MsgReady arrives.
const readyTimeout = 5 * time.Second

// Spare is a spawned menu host that is warm and waiting for MsgOpen, either
// because it has just announced MsgReady or because it closed a menu and
// parked itself. The bar that leases it attaches to its wire directly; the
// broker only spawns, hands over, takes back and reaps.
type Spare struct {
	panel *katnip.Panel
	mon   outputs.Monitor
	id    uint64
	done  chan struct{} // closed once the process has been reaped
	once  sync.Once     // guards the unmap
}

// Broker owns every menu panel on the desktop: it keeps the warm spares,
// leases them to bars and reaps them afterwards. Bars have no pool of their
// own — see package lease for why the pooling lives here.
type Broker struct {
	mu      sync.Mutex
	idle    map[string][]*Spare
	warming map[string]int
	leased  map[uint64]*Spare
	nextID  uint64
	closed  bool

	pool PoolSettings
	// bars is every output with a bar on it, in compositor order; recent
	// is the same outputs ranked by intent, most recent first.
	bars   []string
	recent []string
	want   map[string]int
	settle *time.Timer

	// Seams for tests, which have neither a compositor nor a kitty.
	spawn    func(outputs.Monitor) (*katnip.Panel, error)
	ready    func(*Spare) bool
	monitors func() ([]outputs.Monitor, error)
}

func NewBroker(pool PoolSettings) *Broker {
	return &Broker{
		idle:     make(map[string][]*Spare),
		warming:  make(map[string]int),
		leased:   make(map[uint64]*Spare),
		want:     make(map[string]int),
		pool:     pool,
		spawn:    spawnHostPanel,
		monitors: outputs.GetMonitors,
	}
}

// SetOutputs names the monitors that have a bar. Spares anywhere else are
// killed: a panel is pinned to its output for life, so one on a monitor
// nobody can click is a process spent on nothing.
func (b *Broker) SetOutputs(names []string) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.bars = slices.Clone(names)

	var stale []*Spare
	for output, queue := range b.idle {
		if slices.Contains(b.bars, output) {
			continue
		}
		stale = append(stale, queue...)
		delete(b.idle, output)
	}
	b.recent = slices.DeleteFunc(b.recent, func(o string) bool {
		return !slices.Contains(b.bars, o)
	})
	b.mu.Unlock()

	for _, sp := range stale {
		go sp.kill()
	}
	b.kick()
}

// WarmFor records that the pointer is on output, which is the pool's cue to
// move the spares there.
func (b *Broker) WarmFor(output string) {
	b.touch(output)
	b.kick()
}

// touch fronts the output the user just showed intent for. Leaving one never
// demotes it: with the pointer mid-screen, the bar it just left is still the
// likeliest next click.
func (b *Broker) touch(output string) {
	if output == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	recent := make([]string, 0, len(b.recent)+1)
	recent = append(recent, output)
	for _, o := range b.recent {
		if o != output {
			recent = append(recent, o)
		}
	}
	b.recent = recent
}

// Acquire leases a warm panel on output and returns its lease id and the
// stream the bar should attach to. An empty pool (or a spare warmed before
// the monitor changed mode) costs a kitty spawn on this path.
func (b *Broker) Acquire(output string) (uint64, string, error) {
	// Opening a menu is the strongest statement of intent there is.
	b.touch(output)
	mon, ok := b.monitor(output)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, "", errors.New("menus: broker is shutting down")
	}
	var sp *Spare
	for {
		queue := b.idle[output]
		if len(queue) == 0 {
			break
		}
		candidate := queue[len(queue)-1]
		b.idle[output] = queue[:len(queue)-1]
		// A spare latches its output's scale while warming up, so one warmed
		// before a mode change would open at the wrong metrics.
		if ok && !sameGeometry(candidate.mon, mon) {
			logging.Log.Debug().Msgf("menus: dropping %s spare warmed at the old mode", output)
			go candidate.kill()
			continue
		}
		sp = candidate
		break
	}
	b.mu.Unlock()

	if sp == nil {
		logging.Log.Debug().Msgf("menus: no warm spare for %s, spawning one now", output)
		var err error
		if sp, err = b.warm(mon); err != nil {
			return 0, "", err
		}
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		sp.kill()
		return 0, "", errors.New("menus: broker is shutting down")
	}
	b.nextID++
	sp.id = b.nextID
	b.leased[sp.id] = sp
	id, path := sp.id, sp.panel.ShmPath()
	b.mu.Unlock()

	b.kick()
	return id, path, nil
}

// Release takes a leased panel back. warm says the panel answered MsgClose by
// parking itself off-screen, so it is a spare again and goes straight back to
// the pool: a menu opened and closed then costs no spawn at all. Without it
// there is no telling what state the panel is in, and it is killed.
func (b *Broker) Release(id uint64, warm bool) {
	b.mu.Lock()
	sp := b.leased[id]
	delete(b.leased, id)
	if sp == nil {
		b.mu.Unlock()
		return
	}
	// The lease id is spent: a late release must not reach the panel once
	// somebody else has it.
	sp.id = 0
	output := sp.mon.Name
	recycle := warm && !b.closed && sp.alive() && b.live(output) < b.want[output]
	if recycle {
		b.idle[output] = append(b.idle[output], sp)
	}
	b.mu.Unlock()

	if !recycle {
		sp.kill()
	}
	b.kick()
}

// ReleaseAll reclaims everything a bar held, for when the bar itself is gone.
// Its panels are still showing menus, so none of them is warm.
func (b *Broker) ReleaseAll(ids []uint64) {
	for _, id := range ids {
		b.Release(id, false)
	}
}

// Rehost points the broker at a new source of panels and throws away every
// panel the old one produced. Used when the shared kitty instance is
// replaced: its panels died with it, so nothing here is worth keeping.
//
// A nil source suspends the pool. That is an instance that has gone away, or
// one the first bar has yet to start: there is nowhere to put a panel, and
// spawning one a process at a time is exactly what the instance avoids.
func (b *Broker) Rehost(spawn func(outputs.Monitor) (*katnip.Panel, error)) {
	b.mu.Lock()
	b.spawn = spawn
	var stale []*Spare
	for _, queue := range b.idle {
		stale = append(stale, queue...)
	}
	for _, sp := range b.leased {
		stale = append(stale, sp)
	}
	b.idle = make(map[string][]*Spare)
	b.leased = make(map[uint64]*Spare)
	clear(b.warming)
	b.mu.Unlock()

	for _, sp := range stale {
		sp.free()
	}
	b.kick()
}

// Close kills every panel the broker owns. Leased ones go too: the bars are
// stopping with the supervisor.
func (b *Broker) Close() {
	b.mu.Lock()
	b.closed = true
	if b.settle != nil {
		b.settle.Stop()
	}
	var all []*Spare
	for _, queue := range b.idle {
		all = append(all, queue...)
	}
	for _, sp := range b.leased {
		all = append(all, sp)
	}
	b.idle = make(map[string][]*Spare)
	b.leased = make(map[uint64]*Spare)
	b.mu.Unlock()

	var wg sync.WaitGroup
	for _, sp := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sp.kill()
		}()
	}
	wg.Wait()
}

// kick asks for a rebalance once the plan has stopped moving.
func (b *Broker) kick() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if b.settle == nil {
		b.settle = time.AfterFunc(settleDelay, b.rebalance)
		return
	}
	b.settle.Reset(settleDelay)
}

// rebalance brings the pool in line with the current plan: reclaim what an
// output no longer deserves, spawn what it lacks, in the background so it
// never blocks an open.
//
// Draining and filling off one snapshot can briefly overshoot the budget by
// panels that are still shutting down. That beats leaving the monitor under
// the pointer with nothing.
func (b *Broker) rebalance() {
	b.mu.Lock()
	if b.closed || b.spawn == nil {
		b.mu.Unlock()
		return
	}
	order := b.priority()
	b.want = plan(order, b.pool.Target, b.pool.Max)

	var stale []*Spare
	for output, queue := range b.idle {
		surplus := min(b.live(output)-b.want[output], len(queue))
		if surplus <= 0 {
			continue
		}
		// Oldest first: a spare warmed more recently is the more likely to
		// still match its output's mode.
		stale = append(stale, queue[:surplus]...)
		b.idle[output] = slices.Clone(queue[surplus:])
	}

	need := make(map[string]int, len(order))
	for _, output := range order {
		if n := b.want[output] - b.live(output); n > 0 {
			need[output] = n
			b.warming[output] += n
		}
	}
	b.mu.Unlock()

	for _, sp := range stale {
		go sp.kill()
	}
	for output, n := range need {
		mon, _ := b.monitor(output)
		for range n {
			go b.warmOne(output, mon)
		}
	}
}

// warmOne spawns a single spare and files it, unless the plan moved on while
// it was starting; it can only ever open on its own output.
func (b *Broker) warmOne(output string, mon outputs.Monitor) {
	sp, err := b.warm(mon)

	b.mu.Lock()
	b.warming[output]--
	keep := err == nil && !b.closed && b.live(output) < b.want[output]
	if keep {
		b.idle[output] = append(b.idle[output], sp)
	}
	b.mu.Unlock()

	switch {
	case err != nil:
		logging.Log.Warn().Msgf("menus: warming a spare on %s: %v", output, err)
	case !keep:
		sp.kill()
	}
}

// live counts the panels an output has: warm, warming, and the ones a bar is
// showing. A leased panel holds its output's budget rather than being
// replaced while it is open, because it comes back (see Release) and because
// max is meant to cap the panels that exist, not just the idle ones.
//
// Caller holds b.mu.
func (b *Broker) live(output string) int {
	n := len(b.idle[output]) + b.warming[output]
	for _, sp := range b.leased {
		if sp.mon.Name == output {
			n++
		}
	}
	return n
}

// priority ranks the outputs the pool may warm on: most recent intent first,
// then the bars nobody has pointed at yet, primary first.
//
// Caller holds b.mu.
func (b *Broker) priority() []string {
	order := make([]string, 0, len(b.bars))
	for _, o := range b.recent {
		if slices.Contains(b.bars, o) {
			order = append(order, o)
		}
	}
	rest := make([]string, 0, len(b.bars))
	for _, o := range b.bars {
		if !slices.Contains(order, o) {
			rest = append(rest, o)
		}
	}
	if primary := b.primary(); primary != "" {
		if i := slices.Index(rest, primary); i > 0 {
			rest = slices.Insert(slices.Delete(rest, i, i+1), 0, primary)
		}
	}
	return append(order, rest...)
}

func (b *Broker) warm(mon outputs.Monitor) (*Spare, error) {
	b.mu.Lock()
	spawn, ready := b.spawn, b.ready
	b.mu.Unlock()
	if spawn == nil {
		return nil, errors.New("menus: no kitty instance to put a panel in")
	}
	return warmSpare(mon, spawn, ready)
}

// warmSpare spawns a menu host and waits for it to announce readiness.
func warmSpare(mon outputs.Monitor, spawn func(outputs.Monitor) (*katnip.Panel, error), ready func(*Spare) bool) (*Spare, error) {
	panel, err := spawn(mon)
	if err != nil {
		return nil, err
	}
	sp := &Spare{panel: panel, mon: mon, done: make(chan struct{})}
	go func() {
		panel.Wait()
		// A reader has no other way to notice the process is gone. End the
		// stream before announcing the reap: done is what lets another
		// goroutine unmap the wire, and this still touches it.
		panel.EndStream()
		close(sp.done)
	}()

	if !sp.awaitReady(ready) {
		sp.kill()
		return nil, errors.New("menus: spare died before ready")
	}
	return sp, nil
}

// monitor resolves an output name to its current geometry.
func (b *Broker) monitor(name string) (outputs.Monitor, bool) {
	if name == "" {
		return outputs.Monitor{}, false
	}
	monitors, err := b.monitors()
	if err != nil {
		logging.Log.Debug().Msgf("menus: monitor query failed (%v); spawning unpinned", err)
		return outputs.Monitor{}, false
	}
	for _, m := range monitors {
		if m.Name == name {
			return m, true
		}
	}
	return outputs.Monitor{}, false
}

// primary is where the spares wait until a bar reports the pointer.
func (b *Broker) primary() string {
	monitors, err := b.monitors()
	if err != nil || len(monitors) == 0 {
		return ""
	}
	for _, m := range monitors {
		if m.IsPrimary {
			return m.Name
		}
	}
	return monitors[0].Name
}

// awaitReady blocks until the spare sends MsgReady, or the wire ends. The
// spare stays silent until it receives MsgOpen, so the bar that leases it
// attaches to a stream positioned exactly after MsgReady.
func (sp *Spare) awaitReady(seam func(*Spare) bool) bool {
	if seam != nil {
		return seam(sp)
	}
	// A spare that never warms up must not wedge this goroutine: killing it
	// ends the stream, which is what unblocks the decode below.
	late := time.AfterFunc(readyTimeout, func() { sp.panel.Kill() })
	defer late.Stop()

	dec := cbor.NewDecoder(sp.panel.Reader())
	for {
		var m wire.Msg
		if err := dec.Decode(&m); err != nil {
			return false
		}
		if m.Type == wire.MsgReady {
			return true
		}
	}
}

// shutdown asks the panel to exit and escalates to SIGKILL if it does not,
// returning once the process has been reaped.
func (sp *Spare) shutdown() {
	sp.panel.Stop()
	select {
	case <-sp.done:
		return
	case <-time.After(closeWait):
	}
	logging.Log.Warn().Msg("menus: panel ignored close, killing")
	sp.panel.Kill()
	select {
	case <-sp.done:
	case <-time.After(killWait):
		logging.Log.Error().Msg("menus: panel survived kill")
	}
}

// alive reports whether the panel process is still there.
func (sp *Spare) alive() bool {
	select {
	case <-sp.done:
		return false
	default:
		return true
	}
}

// kill reclaims a spare for good: stop the process, reap it, drop the wire.
func (sp *Spare) kill() {
	sp.shutdown()
	sp.free()
}

// free unmaps the spare's wire, once the process is reaped and nothing here
// is still reading it — the memory goes away under a concurrent read.
func (sp *Spare) free() {
	select {
	case <-sp.done:
	case <-time.After(killWait):
		logging.Log.Error().Msg("menus: leaving a panel's wire mapped, its process will not die")
		return
	}
	sp.once.Do(sp.panel.Close)
}

// sameGeometry reports whether a spare warmed for a is still right for b.
func sameGeometry(a, b outputs.Monitor) bool {
	return a.ScaledWidth == b.ScaledWidth && a.ScaledHeight == b.ScaledHeight && a.Scale == b.Scale
}
