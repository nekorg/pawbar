// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package title

import (
	"strings"
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services/hypr"
)

// hyprEvents are every event that can change which window this bar's
// monitor is showing. Names unknown to the running Hyprland never fire.
var hyprEvents = []string{
	"activewindowv2",    // focus moved to another window
	"windowtitle",       // that window renamed itself (address only)
	"windowtitlev2",     // ... with the new title
	"focusedmonv2",      // focus moved to another monitor
	"closewindow",       //
	"movewindowv2",      // a window changed workspace
	"workspacev2",       //
	"activespecial",     // a special workspace covered the monitor
	"activespecialv2",   //
	"monitoradded",      //
	"monitoraddedv2",    //
	"monitorremoved",    //
	"monitorremovedv2",  //
	hypr.EventReconnect, // events were missed
}

// coalesce is how long an event burst is given to settle before the
// backend re-queries: focusing a window emits several events at once.
const coalesce = 15 * time.Millisecond

type hyprBackend struct {
	svc  *hypr.Service
	ev   chan hypr.HyprEvent
	done chan struct{}
	sig  chan struct{}

	// self is the monitor this bar is on; "" (or mode "focused") follows
	// whichever monitor has focus, which is what a single bar wants.
	self string

	mu    sync.RWMutex
	class string
	title string
	// addr is the address of the window on show, so a title change can be
	// applied without asking the compositor anything.
	addr string
}

func newHyprBackend(s *hypr.Service, self string) backend {
	b := &hyprBackend{
		svc:  s,
		ev:   make(chan hypr.HyprEvent, 32),
		done: make(chan struct{}),
		sig:  make(chan struct{}, 1),
		self: self,
	}

	b.refresh()

	for _, e := range hyprEvents {
		b.svc.RegisterChannel(e, b.ev)
	}
	go b.loop()
	return b
}

func (b *hyprBackend) Close() {
	b.svc.UnregisterChannel(b.ev)
	close(b.done)
}

// loop re-resolves the window on an event burst. Which window a monitor
// is showing cannot be read off the event stream — the events are about
// the focused monitor — so it is queried rather than tracked, with the
// one exception of a title change on the window already on show.
func (b *hyprBackend) loop() {
	defer logging.Recover("title.hypr.loop")
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-b.done:
			if timer != nil {
				timer.Stop()
			}
			return

		case e := <-b.ev:
			if e.Event == "windowtitlev2" && b.retitle(e.Data) {
				b.signal() // a rename of the window on show: no IPC needed
				continue
			}
			if timerC == nil {
				timer = time.NewTimer(coalesce)
				timerC = timer.C
			}

		case <-timerC:
			timerC = nil
			b.refresh()
			b.signal()
		}
	}
}

// retitle applies a windowtitlev2 payload when it renames the window
// already on show, and reports whether it did.
func (b *hyprBackend) retitle(data string) bool {
	address, title, ok := strings.Cut(data, ",")
	if !ok {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.addr == "" || !sameAddress(b.addr, address) {
		return false
	}
	b.title = title
	return true
}

// refresh asks the compositor which window this bar's monitor is
// showing: the monitor names its workspace, the workspace names its last
// focused window, and the client list fills in the class.
func (b *hyprBackend) refresh() {
	monitors, err := hypr.GetMonitors()
	if err != nil {
		logging.Log.Warn().Msgf("title: hypr: monitors query: %v", err)
		return
	}
	mon, ok := pickMonitor(monitors, b.self)
	if !ok {
		b.set(Window{}, "")
		return
	}

	wsID := mon.ActiveWorkspace.Id
	if mon.SpecialWorkspace.Id != 0 {
		wsID = mon.SpecialWorkspace.Id
	}

	workspaces, err := hypr.GetWorkspaces()
	if err != nil {
		logging.Log.Warn().Msgf("title: hypr: workspaces query: %v", err)
		return
	}
	clients, err := hypr.GetClients()
	if err != nil {
		logging.Log.Warn().Msgf("title: hypr: clients query: %v", err)
		return
	}

	win, addr := windowOn(wsID, workspaces, clients)
	b.set(win, addr)
}

// pickMonitor returns the monitor a bar follows: its own when it is
// pinned to one, otherwise whichever has focus.
func pickMonitor(monitors []hypr.Monitor, self string) (hypr.Monitor, bool) {
	for _, m := range monitors {
		if m.Disabled {
			continue
		}
		if self == "" && m.Focused || self != "" && m.Name == self {
			return m, true
		}
	}
	return hypr.Monitor{}, false
}

// windowOn returns the window a workspace is showing: its last focused
// one, falling back to the most recently focused client still on it (a
// workspace that has never been focused has no last window).
func windowOn(wsID int, workspaces []hypr.Workspace, clients []hypr.Client) (Window, string) {
	var lastWindow, lastTitle string
	for _, w := range workspaces {
		if w.Id == wsID {
			lastWindow, lastTitle = w.Lastwindow, w.Lastwindowtitle
			break
		}
	}

	if lastWindow != "" && lastWindow != "0x0" {
		for _, c := range clients {
			if c.Address == lastWindow {
				return Window{Title: c.Title, Class: c.Class}, c.Address
			}
		}
		// The window is gone but the workspace still names it; its title
		// is the best answer left.
		return Window{Title: lastTitle}, ""
	}

	best := -1
	for i, c := range clients {
		if c.Workspace.Id != wsID || c.Hidden {
			continue
		}
		if best < 0 || c.FocusHistoryID < clients[best].FocusHistoryID {
			best = i
		}
	}
	if best < 0 {
		return Window{}, ""
	}
	return Window{Title: clients[best].Title, Class: clients[best].Class}, clients[best].Address
}

// sameAddress compares a client address with an event's, which omits the
// 0x prefix.
func sameAddress(a, b string) bool {
	return strings.TrimPrefix(a, "0x") == strings.TrimPrefix(b, "0x")
}

func (b *hyprBackend) set(w Window, addr string) {
	b.mu.Lock()
	b.class, b.title, b.addr = w.Class, w.Title, addr
	b.mu.Unlock()
}

func (b *hyprBackend) signal() {
	select {
	case b.sig <- struct{}{}:
	default:
	}
}

func (b *hyprBackend) Window() Window {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Window{Title: b.title, Class: b.class}
}

func (b *hyprBackend) Events() <-chan struct{} { return b.sig }
