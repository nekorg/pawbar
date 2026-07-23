// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

const (
	// toggleDebounce swallows a re-open that lands just after the same
	// menu closed (e.g. focus loss raced the toggling click).
	toggleDebounce = 300 * time.Millisecond
	// focusGrace is how long a menu tree may be entirely unfocused
	// before it closes; covers the handoff between parent and submenu.
	focusGrace = 250 * time.Millisecond
	// spawnFocusGrace suspends focus-loss closing while a freshly
	// spawned panel is still starting up: the parent loses focus as
	// soon as the new panel maps, but the panel only reports
	// FocusGained once its kitty process and TUI are running.
	spawnFocusGrace = 2 * time.Second
	// closeWait bounds how long we wait for a panel to exit after
	// MsgClose+Stop before killing it.
	closeWait = 500 * time.Millisecond
	killWait  = 200 * time.Millisecond
)

// owner identifies who opened a menu, for toggle and one-at-a-time
// semantics. id is the opening module's Ctx pointer (stable per
// instance); key subdivides it (e.g. tray items by bus name).
type owner struct {
	id  any
	key string
}

type manager struct {
	mu           sync.Mutex
	current      *tree
	lastClosed   owner
	lastClosedAt time.Time
}

var mgr manager

// openRoot serializes menu switching. It returns the new root handle,
// or nil when the request toggled the open menu closed (or was
// debounced against a just-closed one).
func openRoot(o owner, name string, at Anchor, wCells, hCells int, autoClose bool) (*Handle, error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if cur := mgr.current; cur != nil {
		same := cur.owner == o
		mgr.current = nil
		cur.close()
		if same {
			return nil, nil
		}
	} else if mgr.lastClosed == o && time.Since(mgr.lastClosedAt) < toggleDebounce {
		return nil, nil
	}

	x, y, geo := clampRoot(at, wCells, hCells)
	t := &tree{owner: o, autoClose: autoClose, focused: make(map[*Handle]bool)}
	h, err := t.spawn(name, x, y, wCells, hCells, geo)
	if err != nil {
		return nil, err
	}
	mgr.current = t
	return h, nil
}

// noteClosed records a fully-closed tree; called from the root panel's
// waiter goroutine, never with mgr.mu held by the same goroutine.
func (m *manager) noteClosed(t *tree) {
	m.mu.Lock()
	if m.current == t {
		m.current = nil
	}
	m.lastClosed = t.owner
	m.lastClosedAt = time.Now()
	m.mu.Unlock()
}

// tree is one open menu: a root panel plus its chain of submenu panels,
// treated as a single unit by focus tracking and one-at-a-time closing.
type tree struct {
	owner     owner
	autoClose bool

	mu            sync.Mutex
	panels        []*Handle // [0] = root, deeper submenus after
	closed        bool
	focused       map[*Handle]bool
	focusTimer    *time.Timer
	suppressUntil time.Time
}

// spawn starts a panel and appends it to the tree.
func (t *tree) spawn(name string, x, y, wCells, hCells int, geo wire.Geometry) (*Handle, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("menu already closed")
	}
	panel, err := spawnPanel(name, x, y, wCells, hCells)
	if err != nil {
		return nil, err
	}
	// The new panel steals focus from its parent long before it can
	// report FocusGained; don't let that window read as "the menu
	// lost focus".
	t.suppressUntil = time.Now().Add(spawnFocusGrace)
	if t.focusTimer != nil {
		t.focusTimer.Stop()
		t.focusTimer = nil
	}
	h := &Handle{
		panel:  panel,
		tree:   t,
		enc:    cbor.NewEncoder(panel.Writer()),
		msgs:   make(chan wire.Msg, 32),
		done:   make(chan struct{}),
		geo:    geo,
		wCells: wCells,
		hCells: hCells,
	}
	t.panels = append(t.panels, h)
	go h.read()
	go h.wait()
	return h, nil
}

// close tears the whole tree down, deepest panel first. Idempotent and
// bounded: a wedged panel is killed after closeWait.
func (t *tree) close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	panels := append([]*Handle(nil), t.panels...)
	if t.focusTimer != nil {
		t.focusTimer.Stop()
	}
	t.mu.Unlock()

	for i := len(panels) - 1; i >= 0; i-- {
		panels[i].shutdown()
	}
}

// closeBelow closes every panel deeper than h (used when the hovered
// submenu changes). Focus-loss detection is suppressed briefly so the
// focus bouncing back to h doesn't count as the tree losing focus.
func (t *tree) closeBelow(h *Handle) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	idx := -1
	for i, p := range t.panels {
		if p == h {
			idx = i
			break
		}
	}
	if idx < 0 || idx == len(t.panels)-1 {
		t.mu.Unlock()
		return
	}
	deeper := append([]*Handle(nil), t.panels[idx+1:]...)
	t.panels = t.panels[:idx+1]
	t.suppressUntil = time.Now().Add(2 * focusGrace)
	t.mu.Unlock()

	for i := len(deeper) - 1; i >= 0; i-- {
		deeper[i].shutdown()
	}
}

// panelExited reacts to a panel process dying on its own (Esc, crash):
// the root going down takes the tree with it; a submenu going down
// takes only the panels deeper than it.
func (t *tree) panelExited(h *Handle) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	delete(t.focused, h)
	if len(t.panels) > 0 && t.panels[0] == h {
		t.mu.Unlock()
		t.close()
		return
	}
	idx := -1
	for i, p := range t.panels {
		if p == h {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.mu.Unlock()
		return
	}
	deeper := append([]*Handle(nil), t.panels[idx+1:]...)
	t.panels = t.panels[:idx]
	t.suppressUntil = time.Now().Add(2 * focusGrace)
	t.mu.Unlock()

	for i := len(deeper) - 1; i >= 0; i-- {
		deeper[i].shutdown()
	}
}

func (t *tree) focusGained(h *Handle) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.focused[h] = true
	if t.focusTimer != nil {
		t.focusTimer.Stop()
		t.focusTimer = nil
	}
}

func (t *tree) focusLost(h *Handle) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || !t.autoClose {
		return
	}
	t.focused[h] = false
	if time.Now().Before(t.suppressUntil) {
		return
	}
	if t.focusTimer != nil {
		t.focusTimer.Stop()
	}
	t.focusTimer = time.AfterFunc(focusGrace, t.focusTimeout)
}

func (t *tree) focusTimeout() {
	t.mu.Lock()
	if t.closed || time.Now().Before(t.suppressUntil) {
		t.mu.Unlock()
		return
	}
	for _, f := range t.focused {
		if f {
			t.mu.Unlock()
			return
		}
	}
	t.mu.Unlock()
	logging.Log.Debug().Msg("menus: focus left the menu, closing")
	t.close()
}

// Handle is the bar process's grip on one live menu panel.
type Handle struct {
	panel  *katnip.Panel
	tree   *tree
	enc    *cbor.Encoder
	encMu  sync.Mutex
	msgs   chan wire.Msg
	done   chan struct{}
	geoMu  sync.Mutex
	geo    wire.Geometry
	wCells int
	hCells int
}

// Send delivers a message to the panel.
func (h *Handle) Send(m wire.Msg) error {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	return h.enc.Encode(m)
}

// Messages streams the panel's non-lifecycle messages (clicks, hovers,
// submenu requests). The channel closes when the panel exits; slow
// consumers lose messages rather than blocking the reader.
func (h *Handle) Messages() <-chan wire.Msg { return h.msgs }

// Done closes when this panel's process has exited.
func (h *Handle) Done() <-chan struct{} { return h.done }

// Close closes the whole menu this panel belongs to.
func (h *Handle) Close() { h.tree.close() }

// CloseBelow closes any submenus opened from this panel.
func (h *Handle) CloseBelow() { h.tree.closeBelow(h) }

// OpenSub opens a submenu panel next to h, aligned with the item at
// row, replacing any deeper submenu chain.
func (h *Handle) OpenSub(name string, row, wCells, hCells int) (*Handle, error) {
	h.tree.closeBelow(h)
	h.geoMu.Lock()
	geo, pw := h.geo, h.wCells
	h.geoMu.Unlock()
	x, y, subGeo := placeSubmenu(geo, pw, row, wCells, hCells)
	return h.tree.spawn(name, x, y, wCells, hCells, subGeo)
}

// Geometry returns the panel's current placement.
func (h *Handle) Geometry() wire.Geometry {
	h.geoMu.Lock()
	defer h.geoMu.Unlock()
	return h.geo
}

// shutdown asks the panel to exit and escalates to SIGKILL if it
// doesn't within the bounded waits.
func (h *Handle) shutdown() {
	h.Send(wire.Msg{Type: wire.MsgClose})
	h.panel.Stop()
	select {
	case <-h.done:
		return
	case <-time.After(closeWait):
	}
	logging.Log.Warn().Msg("menus: panel ignored close, killing")
	h.panel.Kill()
	select {
	case <-h.done:
	case <-time.After(killWait):
		logging.Log.Error().Msg("menus: panel survived kill")
	}
}

// read pumps child->parent messages: lifecycle ones go to the tree,
// the rest to Messages().
func (h *Handle) read() {
	dec := cbor.NewDecoder(h.panel.Reader())
	defer close(h.msgs)
	for {
		var m wire.Msg
		if err := dec.Decode(&m); err != nil {
			if !errors.Is(err, io.EOF) {
				logging.Log.Warn().Msgf("menus: decoding panel message: %v", err)
			}
			return
		}
		switch m.Type {
		case wire.MsgFocusGained:
			h.tree.focusGained(h)
		case wire.MsgFocusLost:
			h.tree.focusLost(h)
		case wire.MsgResized:
			h.geoMu.Lock()
			if m.Geo != nil {
				h.geo = *m.Geo
			}
			if m.Cols > 0 {
				h.wCells = m.Cols
			}
			if m.Rows > 0 {
				h.hCells = m.Rows
			}
			h.geoMu.Unlock()
		default:
			if m.Type == wire.MsgSubmenuReq && m.Geo != nil && m.Geo.PPCX > 0 && m.Geo.PPCY > 0 {
				// The panel measured its own cell metrics; they beat
				// the bar-derived estimate for placing its submenu.
				h.geoMu.Lock()
				h.geo.PPCX = m.Geo.PPCX
				h.geo.PPCY = m.Geo.PPCY
				h.geoMu.Unlock()
			}
			select {
			case h.msgs <- m:
			default:
				logging.Log.Warn().Msg("menus: dropping panel message, consumer too slow")
			}
		}
	}
}

// wait reaps the panel process and propagates the exit to the tree.
func (h *Handle) wait() {
	h.panel.Wait()
	close(h.done)
	t := h.tree
	isRoot := false
	t.mu.Lock()
	if len(t.panels) > 0 && t.panels[0] == h {
		isRoot = true
	}
	t.mu.Unlock()
	t.panelExited(h)
	if isRoot {
		mgr.noteClosed(t)
	}
}
