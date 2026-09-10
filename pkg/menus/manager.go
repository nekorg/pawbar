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
	// spawnFocusGrace suspends focus-loss closing while a just-acquired
	// panel reveals itself: the parent loses focus as soon as the panel
	// is assigned, but the panel only reports FocusGained once it has
	// rendered off-screen and moved on-screen with exclusive focus.
	// Warm spares are pre-mapped, so this only covers the ~200ms reveal.
	spawnFocusGrace = 1 * time.Second
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
	cur := mgr.current
	same := cur != nil && cur.owner == o
	if cur != nil {
		mgr.current = nil
	}
	debounced := cur == nil && mgr.lastClosed == o && time.Since(mgr.lastClosedAt) < toggleDebounce
	mgr.mu.Unlock()

	// Tearing the old menu down and getting a panel for the new one are both
	// slow — a panel that ignores MsgClose, a pool that has to spawn — and
	// neither is worth stalling every other menu on.
	if cur != nil {
		cur.close()
	}
	if same || debounced {
		return nil, nil
	}

	p, err := acquire(name)
	if err != nil {
		return nil, err
	}
	_, _, geo := clampRoot(at, wCells, hCells)
	t := &tree{owner: o, autoClose: autoClose, focused: make(map[*Handle]bool)}
	h, err := t.spawn(p, name, wCells, hCells, geo)
	if err != nil {
		p.release(false)
		p.free()
		return nil, err
	}

	mgr.mu.Lock()
	raced := mgr.current
	mgr.current = t
	mgr.mu.Unlock()
	if raced != nil {
		// Two modules opened a menu at once. Whoever got here last is the one
		// the user is looking at; the other goes away.
		raced.close()
	}
	return h, nil
}

// acquire gets a panel to put a menu in, from the supervisor when there is
// one. It runs with no menu lock held: on a pool miss it costs a kitty spawn.
func acquire(kind string) (*panelConn, error) {
	start := time.Now()
	p, err := panels().acquire()
	if err != nil {
		return nil, err
	}
	logging.Log.Debug().Msgf("menus: got a panel for %q in %s", kind, time.Since(start))
	return p, nil
}

// noteClosed records a fully-closed tree; called from the root panel's
// reader goroutine, never with mgr.mu held by the same goroutine.
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

// spawn assigns an acquired panel a menu (kind, size, placement) via MsgOpen
// and appends it to the tree. The caller then streams content (MsgUpdate),
// which the host renders off-screen before revealing itself on-screen.
func (t *tree) spawn(p *panelConn, kind string, wCells, hCells int, geo wire.Geometry) (*Handle, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("menu already closed")
	}
	// The panel grabs focus from its parent long before it can report
	// FocusGained; don't let that window read as "the menu lost focus".
	t.suppressUntil = time.Now().Add(spawnFocusGrace)
	if t.focusTimer != nil {
		t.focusTimer.Stop()
		t.focusTimer = nil
	}
	h := &Handle{
		conn:   p,
		tree:   t,
		msgs:   make(chan wire.Msg, 32),
		done:   make(chan struct{}),
		geo:    geo,
		wCells: wCells,
		hCells: hCells,
	}
	if err := h.Send(wire.Msg{Type: wire.MsgOpen, Kind: kind, Geo: &geo, Cols: wCells, Rows: hCells}); err != nil {
		logging.Log.Warn().Msgf("menus: sending open: %v", err)
	}
	t.panels = append(t.panels, h)
	go h.read()
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

// Handle is the bar process's grip on one live menu panel. The wire comes
// from whoever supplied the panel, already positioned after MsgReady, and
// this is its only reader.
type Handle struct {
	conn  *panelConn
	tree  *tree
	encMu sync.Mutex
	gone  bool // wire dropped; guarded by encMu
	// warm records that the panel parked itself rather than died. Written
	// by read, read by exited, which is read's own defer.
	warm   bool
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
	if h.gone {
		return errors.New("menus: the panel is gone")
	}
	return h.conn.enc.Encode(m)
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
	_, _, subGeo := placeSubmenu(geo, pw, row, wCells, hCells)
	p, err := acquire(name)
	if err != nil {
		return nil, err
	}
	sub, err := h.tree.spawn(p, name, wCells, hCells, subGeo)
	if err != nil {
		p.release(false)
		p.free()
		return nil, err
	}
	return sub, nil
}

// Geometry returns the panel's current placement.
func (h *Handle) Geometry() wire.Geometry {
	h.geoMu.Lock()
	defer h.geoMu.Unlock()
	return h.geo
}

// shutdown asks the panel to close its menu. A live one answers by parking
// itself off-screen and sending MsgClosed, which is what hands the wire back
// (see exited) and what makes it reusable instead of dead.
//
// A menu app wedged mid-frame never reads MsgClose, so the hand-back is also
// armed on a timer: releasing without the warm flag tells the owner to kill
// it, which ends the wire and gets here the other way.
func (h *Handle) shutdown() {
	h.Send(wire.Msg{Type: wire.MsgClose})
	time.AfterFunc(closeWait, func() { h.conn.release(false) })
}

// read pumps child->parent messages: lifecycle ones go to the tree,
// the rest to Messages(). The wire ends either because the panel parked
// itself (MsgClosed) or because its process is gone and its owner ended the
// stream; both reach the tree through exited.
func (h *Handle) read() {
	defer h.exited()
	defer close(h.msgs)
	dec := h.conn.dec
	for {
		var m wire.Msg
		if err := dec.Decode(&m); err != nil {
			if !errors.Is(err, io.EOF) {
				logging.Log.Warn().Msgf("menus: decoding panel message: %v", err)
			}
			return
		}
		switch m.Type {
		case wire.MsgClosed:
			// The panel is warm again and no longer ours. Stop reading so
			// its owner can: the wire has one read cursor.
			h.warm = true
			return
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
				// The panel measured its own cell metrics; they beat the
				// bar-derived estimate for placing its submenu.
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

// exited runs once the wire has ended: the panel is gone or parked, so the
// tree is told, this process's mapping of the wire is dropped and the panel
// is handed back. Nothing reads it after read() returns, which is what makes
// unmapping safe and what lets the owner read the wire next.
func (h *Handle) exited() {
	close(h.done)

	h.encMu.Lock()
	h.gone = true
	h.encMu.Unlock()
	h.conn.free()
	h.conn.release(h.warm)

	t := h.tree
	t.mu.Lock()
	isRoot := len(t.panels) > 0 && t.panels[0] == h
	t.mu.Unlock()

	t.panelExited(h)
	if isRoot {
		mgr.noteClosed(t)
	}
}
