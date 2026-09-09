// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"errors"
	"sync"
	"time"

	"github.com/codelif/outputs"
	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

// poolTarget is how many warm spares to keep hot. One spare keeps the
// invariant "live panels = visible + 1": opening a menu (or submenu)
// consumes the spare and refills in the background. The second covers the
// submenu that follows without waiting for that refill.
//
// This is a desktop-wide number, not a per-monitor one. A panel's output is
// fixed when kitty creates it, so a spare only ever serves the monitor it
// was warmed on — but there is one mouse cursor, so only the bar under it
// can be clicked next, and the spares live there.
const poolTarget = 2

// readyTimeout bounds waiting for a freshly spawned spare to warm up
// (kitty spawn + vaxis handshake + off-screen map). Generous; the wait
// returns as soon as MsgReady arrives.
const readyTimeout = 5 * time.Second

// Spare is a spawned menu host that has announced MsgReady and is waiting
// for MsgOpen. The bar that leases it attaches to its wire directly; the
// broker only spawns, hands over and reaps.
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
	warmFor string // the output the spares live on
	nextID  uint64
	closed  bool

	// Seams for tests, which have neither a compositor nor a kitty.
	spawn    func(outputs.Monitor) (*katnip.Panel, error)
	ready    func(*Spare) bool
	monitors func() ([]outputs.Monitor, error)
}

func NewBroker() *Broker {
	return &Broker{
		idle:     make(map[string][]*Spare),
		warming:  make(map[string]int),
		leased:   make(map[uint64]*Spare),
		spawn:    spawnHostPanel,
		monitors: outputs.GetMonitors,
	}
}

// Warm starts the pool where the pointer has not been reported yet: the
// primary output, which is where a compositor puts an unpinned panel too.
func (b *Broker) Warm() { b.ensure() }

// WarmFor moves the spares to output, which is where the pointer now is.
// Spares warmed for anywhere else are killed: they cannot be revealed on
// another monitor, so holding them only costs a kitty process each.
func (b *Broker) WarmFor(output string) {
	if output == "" {
		return
	}
	b.mu.Lock()
	if b.closed || b.warmFor == output {
		b.mu.Unlock()
		b.ensure()
		return
	}
	b.warmFor = output
	stale := b.takeIdleElsewhere()
	b.mu.Unlock()

	for _, sp := range stale {
		sp.kill()
	}
	b.ensure()
}

// Acquire leases a warm panel on output and returns its lease id and the
// stream the bar should attach to. An empty pool (or a spare warmed before
// the monitor changed mode) costs a kitty spawn on this path.
func (b *Broker) Acquire(output string) (uint64, string, error) {
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

	b.ensure()
	return id, path, nil
}

// Release reclaims a leased panel: the bar has already asked it to close, so
// this is the escalation and the reap.
func (b *Broker) Release(id uint64) {
	b.mu.Lock()
	sp := b.leased[id]
	delete(b.leased, id)
	b.mu.Unlock()
	if sp == nil {
		return
	}
	sp.kill()
}

// ReleaseAll reclaims everything a bar held, for when the bar itself is gone.
func (b *Broker) ReleaseAll(ids []uint64) {
	for _, id := range ids {
		b.Release(id)
	}
}

// Drop kills the spares warmed for an output that is no longer usable (a
// disconnected monitor); a returning monitor gets freshly warmed ones.
func (b *Broker) Drop(output string) {
	b.mu.Lock()
	stale := b.idle[output]
	delete(b.idle, output)
	if b.warmFor == output {
		b.warmFor = ""
	}
	b.mu.Unlock()

	for _, sp := range stale {
		sp.kill()
	}
}

// Close kills every panel the broker owns. Leased ones go too: the bars are
// stopping with the supervisor.
func (b *Broker) Close() {
	b.mu.Lock()
	b.closed = true
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

// ensure tops the pool up to poolTarget on the output the pointer is on,
// spawning in the background so it never blocks an open.
func (b *Broker) ensure() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	if b.warmFor == "" {
		b.warmFor = b.primary()
	}
	output := b.warmFor
	need := poolTarget - len(b.idle[output]) - b.warming[output]
	if output == "" || need <= 0 {
		b.mu.Unlock()
		return
	}
	b.warming[output] += need
	b.mu.Unlock()

	mon, _ := b.monitor(output)
	for range need {
		go func() {
			sp, err := b.warm(mon)

			b.mu.Lock()
			b.warming[output]--
			keep := err == nil && !b.closed && b.warmFor == output
			if keep {
				b.idle[output] = append(b.idle[output], sp)
			}
			b.mu.Unlock()

			switch {
			case err != nil:
				logging.Log.Warn().Msgf("menus: warming a spare on %s: %v", output, err)
			case !keep:
				// The pointer moved on (or we are shutting down) while this
				// one was spawning; it can only ever open on its own output.
				sp.kill()
			}
		}()
	}
}

func (b *Broker) warm(mon outputs.Monitor) (*Spare, error) {
	return warmSpare(mon, b.spawn, b.ready)
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

// takeIdleElsewhere empties every idle queue but the one being warmed.
// Caller holds b.mu.
func (b *Broker) takeIdleElsewhere() []*Spare {
	var stale []*Spare
	for output, queue := range b.idle {
		if output == b.warmFor {
			continue
		}
		stale = append(stale, queue...)
		delete(b.idle, output)
	}
	return stale
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
