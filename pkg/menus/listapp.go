// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"image/color"
	"time"

	"github.com/nekorg/pawbar/pkg/menus/wire"
	"go.rockorager.dev/vaxis"
)

// hoverActivationTimeout is how long the pointer rests on a submenu
// item before the submenu opens.
const hoverActivationTimeout = 200 * time.Millisecond

func init() {
	Register("pawmenu", listApp)
}

// listState is the child-side selection/hover state of one list panel.
type listState struct {
	items          []wire.Item
	row            int // selected row (pointer or keys), -1 = none
	mousePressed   bool
	mouseOnSurface bool
	hoverTimer     *time.Timer
	hoverItemID    int32
}

func (st *listState) cancelHoverTimer() {
	if st.hoverTimer != nil {
		st.hoverTimer.Stop()
		st.hoverTimer = nil
	}
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
	st.cancelHoverTimer()
	next := st.row + delta
	for st.validRow(next) && st.items[next].Separator {
		next += delta
	}
	if st.validRow(next) {
		st.row = next
	}
}

// listApp is the one generic list-menu TUI; every list panel (root or
// submenu) runs it, fed items over the wire.
func listApp(s *Session) int {
	st := &listState{row: -1}

	c := s.Vx().QueryForeground()
	rgb := c.Params()
	fg := color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}

	r := newListRenderer(s.Window(), fg)

	// submenuReq carries this panel's measured cell metrics so the bar
	// places the submenu against real row positions, not estimates.
	submenuReq := func(id int32, row int) wire.Msg {
		m := wire.Msg{Type: wire.MsgSubmenuReq, ItemID: id, Row: row}
		if px, py := s.MeasuredPPC(); px > 0 && py > 0 {
			m.Geo = &wire.Geometry{PPCX: px, PPCY: py}
		}
		return m
	}

	hover := func() {
		it := st.current()
		if it == nil {
			return
		}
		s.Send(wire.Msg{Type: wire.MsgHovered, ItemID: it.ID})
		if it.HasSubmenu && !it.Disabled {
			if st.hoverItemID != it.ID {
				if st.hoverItemID != 0 {
					s.Send(wire.Msg{Type: wire.MsgSubmenuCancel, ItemID: st.hoverItemID})
				}
				st.hoverItemID = it.ID
				id, row := it.ID, st.row
				st.hoverTimer = time.AfterFunc(hoverActivationTimeout, func() {
					if st.hoverItemID == id && st.row == row {
						s.Send(submenuReq(id, row))
					}
				})
			}
		} else if st.hoverItemID != 0 {
			s.Send(wire.Msg{Type: wire.MsgSubmenuCancel, ItemID: st.hoverItemID})
			st.hoverItemID = 0
		}
	}

	click := func() {
		if !st.mouseOnSurface {
			// Released outside every menu surface: tell the bar so it
			// can close the whole tree.
			s.Send(wire.Msg{Type: wire.MsgClicked, ItemID: -1})
			return
		}
		if it := st.current(); it != nil && st.selectable(st.row) {
			s.Send(wire.Msg{Type: wire.MsgClicked, ItemID: it.ID})
		}
	}

	requestSubmenu := func() {
		if it := st.current(); it != nil && it.HasSubmenu && !it.Disabled {
			st.cancelHoverTimer()
			s.Send(submenuReq(it.ID, st.row))
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
					st.mouseOnSurface = false
					st.row = -1
					st.cancelHoverTimer()
				case vaxis.EventMotion:
					if st.row != ev.Row || !st.mouseOnSurface {
						st.mouseOnSurface = true
						st.row = ev.Row
						st.cancelHoverTimer()
						if st.selectable(st.row) {
							hover()
						} else if st.hoverItemID != 0 {
							s.Send(wire.Msg{Type: wire.MsgSubmenuCancel, ItemID: st.hoverItemID})
							st.hoverItemID = 0
						}
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
					hover()
					draw(false)
				case ev.Keycode == vaxis.KeyDown || ev.Keycode == 'j':
					st.navigate(+1)
					hover()
					draw(false)
				case ev.Keycode == vaxis.KeyEnter:
					if st.selectable(st.row) {
						s.Send(wire.Msg{Type: wire.MsgClicked, ItemID: st.items[st.row].ID})
					}
				case ev.Keycode == vaxis.KeyRight:
					requestSubmenu()
				case ev.Keycode == vaxis.KeyLeft:
					return 0
				}
			}

		case m, ok := <-s.Messages():
			if !ok {
				return 0
			}
			if m.Type != wire.MsgUpdate {
				continue
			}
			st.items = m.Items
			if !st.validRow(st.row) {
				st.row = -1
			}
			// New items mean new wire IDs; drop the stale icon cache.
			r = newListRenderer(s.Window(), fg)
			r.win.Clear()
			draw(true)
			w, h := listDims(st.items)
			cols, rows := r.win.Size()
			if cols != w || rows != h {
				s.Resize(w, h)
			}
		}
	}
}
