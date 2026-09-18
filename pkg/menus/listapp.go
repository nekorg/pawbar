// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"time"

	"github.com/nekorg/pawbar/pkg/menus/wire"
	"go.rockorager.dev/vaxis"
)

const (
	// hoverActivationTimeout is how long the pointer rests on a submenu
	// item before the submenu opens.
	hoverActivationTimeout = 200 * time.Millisecond
	// submenuCloseGrace is how long an open submenu outlives the pointer
	// straying off the row it hangs from. The run into a submenu clips the
	// rows between, and a hand correcting itself is back well inside this;
	// closing on the first stray row killed the menu just before the
	// pointer got there.
	submenuCloseGrace = 150 * time.Millisecond
)

func init() {
	Register("pawmenu", listApp)
}

// listState is the child-side selection/hover state of one list panel.
type listState struct {
	items          []wire.Item
	row            int // selected row (pointer or keys), -1 = none
	mousePressed   bool
	mouseOnSurface bool

	// openTimer counts down to opening pendingID's submenu; closeTimer
	// counts down to cancelling the one already open at openRow. Both are
	// selected on by the loop, so every field here stays on one goroutine.
	openTimer  *time.Timer
	pendingID  int32
	pendingRow int
	closeTimer *time.Timer
	openID     int32
	openRow    int

	send func(wire.Msg)                   // to the bar
	req  func(id int32, row int) wire.Msg // submenu request, with this panel's metrics
}

func newListState() *listState {
	return &listState{row: -1, pendingRow: -1, openRow: -1}
}

// timerC is a nil-safe timer channel: a nil timer blocks forever, which is
// what an unarmed case in a select should do.
func timerC(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// armOpen starts the countdown to opening id's submenu. Already counting
// down for the same row means the pointer only wobbled inside it, so the
// countdown is left alone rather than restarted.
func (st *listState) armOpen(id int32, row int) {
	if st.pendingID == id && st.openTimer != nil {
		return
	}
	st.stopOpen()
	st.pendingID, st.pendingRow = id, row
	st.openTimer = time.NewTimer(hoverActivationTimeout)
}

func (st *listState) stopOpen() {
	if st.openTimer != nil {
		st.openTimer.Stop()
		st.openTimer = nil
	}
	st.pendingID, st.pendingRow = 0, -1
}

// armClose puts the open submenu on notice. Already armed is left alone, so
// a pointer crossing several rows still gets one grace period rather than a
// fresh one per row.
func (st *listState) armClose() {
	if st.openID == 0 || st.closeTimer != nil {
		return
	}
	st.closeTimer = time.NewTimer(submenuCloseGrace)
}

func (st *listState) stopClose() {
	if st.closeTimer != nil {
		st.closeTimer.Stop()
		st.closeTimer = nil
	}
}

// openSub asks the bar for a submenu now. No cancel goes first: OpenSub
// closes whatever hangs below before spawning.
func (st *listState) openSub(id int32, row int) {
	st.stopOpen()
	st.stopClose()
	st.send(st.req(id, row))
	st.openID, st.openRow = id, row
}

// closeSub cancels the open submenu now, grace or no grace.
func (st *listState) closeSub() {
	st.stopClose()
	if st.openID == 0 {
		return
	}
	st.send(wire.Msg{Type: wire.MsgSubmenuCancel, ItemID: st.openID})
	st.openID, st.openRow = 0, -1
}

func (st *listState) validRow(i int) bool {
	return i >= 0 && i < len(st.items)
}

func (st *listState) selectable(i int) bool {
	return st.validRow(i) && !st.items[i].Separator && !st.items[i].Disabled
}

func (st *listState) current() *wire.Item {
	if !st.validRow(st.row) {
		return nil
	}
	return &st.items[st.row]
}

func (st *listState) navigate(delta int) {
	st.stopOpen()
	next := st.row + delta
	for st.validRow(next) && st.items[next].Separator {
		next += delta
	}
	if st.validRow(next) {
		st.row = next
	}
}

// hover reacts to the selection landing on st.row.
func (st *listState) hover() {
	it := st.current()
	if it == nil {
		return
	}
	st.send(wire.Msg{Type: wire.MsgHovered, ItemID: it.ID})

	if st.row == st.openRow {
		// Back on the row the open submenu hangs from; it stays.
		st.stopOpen()
		st.stopClose()
		return
	}
	if it.HasSubmenu && !it.Disabled {
		st.armOpen(it.ID, st.row)
	} else {
		st.stopOpen()
	}
	st.armClose()
}

// pointerTo moves the selection to row under the pointer.
func (st *listState) pointerTo(row int) {
	st.mouseOnSurface = true
	st.row = row
	if st.selectable(row) {
		st.hover()
		return
	}
	// A separator or a disabled row: nothing of its own to open, and the
	// open submenu is only being passed over.
	st.stopOpen()
	st.armClose()
}

// pointerLeft handles the pointer leaving this panel's surface. The likely
// destination is the submenu, which this panel cannot see, so an armed
// close would shut the menu out from under the pointer.
func (st *listState) pointerLeft() {
	st.mouseOnSurface = false
	st.row = -1
	st.stopOpen()
	st.stopClose()
}

// openFired opens the submenu the open timer was counting down to.
func (st *listState) openFired() {
	st.openTimer = nil
	if st.pendingID == 0 {
		return
	}
	st.openSub(st.pendingID, st.pendingRow)
}

// closeFired cancels the submenu the close timer was counting down on.
func (st *listState) closeFired() {
	st.closeTimer = nil
	st.closeSub()
}

// idAt is the wire id currently standing at row, or 0.
func (st *listState) idAt(row int) int32 {
	if !st.validRow(row) {
		return 0
	}
	return st.items[row].ID
}

// rebind re-points the open and pending submenus at the ids the new items
// carry. Every refresh reissues this level's ids, and the rows are what
// outlive them.
func (st *listState) rebind() {
	if !st.validRow(st.row) {
		st.row = -1
	}
	if st.openID != 0 {
		if st.openID = st.idAt(st.openRow); st.openID == 0 {
			st.openRow = -1
		}
	}
	if st.pendingID != 0 {
		if st.pendingID = st.idAt(st.pendingRow); st.pendingID == 0 {
			st.stopOpen()
		}
	}
}

// listApp is the one generic list-menu TUI; every list panel (root or
// submenu) runs it, fed items over the wire.
func listApp(s *Session) int {
	st := newListState()
	st.send = func(m wire.Msg) { s.Send(m) }
	// The submenu request carries this panel's measured cell metrics so the
	// bar places the submenu against real row positions, not estimates.
	st.req = func(id int32, row int) wire.Msg {
		m := wire.Msg{Type: wire.MsgSubmenuReq, ItemID: id, Row: row}
		if px, py := s.MeasuredPPC(); px > 0 && py > 0 {
			m.Geo = &wire.Geometry{PPCX: px, PPCY: py}
		}
		return m
	}

	fg := s.Foreground()
	r := newListRenderer(s.Window(), fg)

	click := func() {
		if !st.mouseOnSurface {
			// Released outside every menu surface: tell the bar so it
			// can close the whole tree.
			st.send(wire.Msg{Type: wire.MsgClicked, ItemID: -1})
			return
		}
		if it := st.current(); it != nil && st.selectable(st.row) {
			if st.row != st.openRow {
				// Acting on another row; the open submenu goes now rather
				// than waiting out its grace.
				st.closeSub()
			}
			st.send(wire.Msg{Type: wire.MsgClicked, ItemID: it.ID})
		}
	}

	draw := func(icons bool) {
		r.draw(st, icons)
		s.Render()
	}

	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				return 0
			}
			switch ev := ev.(type) {
			case vaxis.Redraw:
				// Posted (among others) when an icon finishes its
				// async encode; redraw so cached images get placed.
				draw(true)

			case vaxis.Resize:
				r = newListRenderer(s.Window(), fg)
				r.win.Clear()
				draw(true)

			case vaxis.Mouse:
				switch ev.EventType {
				case vaxis.EventLeave:
					st.pointerLeft()
				case vaxis.EventMotion:
					if st.row != ev.Row || !st.mouseOnSurface {
						st.pointerTo(ev.Row)
					}
				case vaxis.EventPress:
					if ev.Button == vaxis.MouseLeftButton {
						st.mousePressed = true
					}
				case vaxis.EventRelease:
					if ev.Button == vaxis.MouseLeftButton {
						st.mousePressed = false
						click()
					}
				}
				draw(false)

			case vaxis.Key:
				if ev.EventType != vaxis.EventPress {
					continue
				}
				switch {
				case ev.Keycode == vaxis.KeyUp || ev.Keycode == 'k':
					st.navigate(-1)
					st.closeSub() // keys are deliberate; no grace
					st.hover()
					draw(false)
				case ev.Keycode == vaxis.KeyDown || ev.Keycode == 'j':
					st.navigate(+1)
					st.closeSub()
					st.hover()
					draw(false)
				case ev.Keycode == vaxis.KeyEnter:
					if st.selectable(st.row) {
						st.send(wire.Msg{Type: wire.MsgClicked, ItemID: st.items[st.row].ID})
					}
				case ev.Keycode == vaxis.KeyRight:
					if it := st.current(); it != nil && it.HasSubmenu && !it.Disabled {
						st.openSub(it.ID, st.row)
					}
				case ev.Keycode == vaxis.KeyLeft:
					return 0
				}
			}

		case <-timerC(st.openTimer):
			st.openFired()
			draw(false)

		case <-timerC(st.closeTimer):
			st.closeFired()
			draw(false)

		case m, ok := <-s.Messages():
			if !ok {
				return 0
			}
			if m.Type != wire.MsgUpdate {
				continue
			}
			st.items = m.Items
			st.rebind()
			// New items mean new wire IDs; drop the stale icon cache.
			r = newListRenderer(s.Window(), fg)
			r.win.Clear()
			draw(true)
			w, h := listDims(st.items)
			cols, rows := r.win.Size()
			if cols != w || rows != h {
				s.Resize(w, h)
			} else {
				// Already the right size (root spawns at its final size),
				// but the bar's initial placement may overflow with a
				// different font; re-clamp against our own metrics.
				s.Reposition(cols, rows)
			}
		}
	}
}
