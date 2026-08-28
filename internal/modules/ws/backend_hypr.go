// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ws

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services/hypr"
)

// hyprEvents are every event that can change what the bar shows. Names
// unknown to the running Hyprland simply never fire, so listing both the
// v2 and the older spellings costs nothing.
var hyprEvents = []string{
	"workspacev2",        // focused workspace changed
	"focusedmonv2",       // focus moved to another monitor
	"moveworkspacev2",    // a workspace moved between monitors
	"createworkspacev2",  //
	"destroyworkspacev2", //
	"renameworkspace",    //
	"activespecial",      // special workspace opened/closed
	"activespecialv2",    //
	"urgent",             // a window demands attention
	"monitoradded",       // hotplug: workspaces get redistributed
	"monitoraddedv2",     //
	"monitorremoved",     //
	"monitorremovedv2",   //
	hypr.EventReconnect,  // events were missed
}

// coalesce is how long the backend waits for an event burst to settle
// before re-querying. Focusing a new workspace emits several events back
// to back; one refresh for the lot keeps the IPC traffic to two calls.
const coalesce = 15 * time.Millisecond

type hyprBackend struct {
	svc  *hypr.Service
	ev   chan hypr.HyprEvent
	done chan struct{}
	sig  chan struct{}

	mu sync.RWMutex
	ws []Workspace
	// urgent is sticky: Hyprland reports urgency as an event, never as
	// state, so it has to survive the refreshes that rebuild everything
	// else. Cleared when the workspace is focused or disappears.
	urgent map[int]bool
}

func newHyprBackend(s *hypr.Service) backend {
	b := &hyprBackend{
		svc:    s,
		ev:     make(chan hypr.HyprEvent, 32),
		done:   make(chan struct{}),
		sig:    make(chan struct{}, 1),
		urgent: make(map[int]bool),
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

// loop rebuilds the workspace list from IPC on every event burst.
//
// Hyprland's events carry too little to maintain a per-monitor view
// incrementally — createworkspacev2 does not say which monitor, and the
// focused monitor changes without any workspace event at all — so the
// cache is rebuilt rather than patched. Two socket round-trips per burst
// buys a list that cannot drift.
func (b *hyprBackend) loop() {
	defer logging.Recover("ws.hypr.loop")
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
			if e.Event == "urgent" {
				b.markUrgent(e.Data)
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

// refresh rebuilds the workspace list: /workspaces for what exists and
// where it lives, /monitors for what is on screen and what has focus.
func (b *hyprBackend) refresh() {
	workspaces, err := hypr.GetWorkspaces()
	if err != nil {
		logging.Log.Warn().Msgf("ws: hypr: workspaces query: %v; keeping cached list", err)
		return
	}
	monitors, err := hypr.GetMonitors()
	if err != nil {
		logging.Log.Warn().Msgf("ws: hypr: monitors query: %v; keeping cached list", err)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.ws = hyprList(workspaces, monitors, b.urgent)
}

// hyprList turns one pair of IPC answers into the bar's workspace list.
// urgent is the sticky urgency map, pruned here of workspaces that were
// focused (the user has seen them) or that no longer exist.
func hyprList(workspaces []hypr.Workspace, monitors []hypr.Monitor, urgent map[int]bool) []Workspace {
	// Every monitor shows one workspace, plus a special one when it is
	// open. The focused monitor's is the focused workspace — the special
	// one when it is open, since that is what has focus then.
	visible := make(map[int]bool, len(monitors)*2)
	focused := 0
	for _, m := range monitors {
		if m.Disabled {
			continue
		}
		if m.ActiveWorkspace.Id != 0 {
			visible[m.ActiveWorkspace.Id] = true
		}
		if m.SpecialWorkspace.Id != 0 {
			visible[m.SpecialWorkspace.Id] = true
		}
		if m.Focused {
			focused = m.ActiveWorkspace.Id
			if m.SpecialWorkspace.Id != 0 {
				focused = m.SpecialWorkspace.Id
			}
		}
	}

	delete(urgent, focused)
	live := make(map[int]bool, len(workspaces))

	list := make([]Workspace, 0, len(workspaces))
	for _, w := range workspaces {
		live[w.Id] = true
		list = append(list, Workspace{
			ID:        w.Id,
			Name:      w.Name,
			Monitor:   w.Monitor,
			MonitorID: w.MonitorID,
			Active:    w.Id == focused,
			Visible:   visible[w.Id],
			Urgent:    urgent[w.Id],
			Special:   strings.HasPrefix(w.Name, "special:"),
		})
	}
	for id := range urgent {
		if !live[id] {
			delete(urgent, id)
		}
	}

	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// markUrgent records the workspace of the window that raised urgency. The
// clients query happens before the lock: IPC must not stall List.
func (b *hyprBackend) markUrgent(address string) {
	clients, err := hypr.GetClients()
	if err != nil {
		logging.Log.Warn().Msgf("ws: hypr: clients query: %v", err)
		return
	}
	for _, c := range clients {
		if strings.TrimPrefix(c.Address, "0x") != address {
			continue
		}
		b.mu.Lock()
		b.urgent[c.Workspace.Id] = true
		b.mu.Unlock()
		return
	}
}

func (b *hyprBackend) signal() {
	select {
	case b.sig <- struct{}{}:
	default:
	}
}

func (b *hyprBackend) List() []Workspace {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Workspace(nil), b.ws...)
}

func (b *hyprBackend) Events() <-chan struct{} { return b.sig }

// Region identifies a workspace in a click. Special workspaces are
// switched by name, and so are named workspaces: Hyprland gives a named
// workspace a negative id (e.g. -1342), and a negative number handed to
// the workspace dispatch is parsed as a relative move rather than an
// absolute workspace id. Only a plain numbered workspace goes by id.
func (b *hyprBackend) Region(w Workspace) string {
	if w.Special {
		return w.Name
	}
	if w.Name != "" && w.Name != strconv.Itoa(w.ID) {
		return w.Name
	}
	return strconv.Itoa(w.ID)
}

// Goto switches to the clicked workspace, focusing its monitor first when
// that is a different one: a bar click should move the user to the screen
// they clicked on, even when the workspace is empty.
func (b *hyprBackend) Goto(region string) {
	target, ok := b.find(region)
	if ok && target.Monitor != "" && !b.monitorFocused(target.Monitor) {
		if err := hypr.FocusMonitor(target.Monitor); err != nil {
			logging.Log.Warn().Msgf("ws: hypr: focus monitor %q: %v", target.Monitor, err)
		}
	}

	if name, isSpecial := strings.CutPrefix(region, "special:"); isSpecial {
		if err := hypr.ToggleSpecialWorkspace(name); err != nil {
			logging.Log.Error().Msgf("ws: hypr: toggle special %q: %v", name, err)
		}
		return
	}
	if err := hypr.GoToWorkspace(region); err != nil {
		logging.Log.Error().Msgf("ws: hypr: goto %q: %v", region, err)
	}
}

func (b *hyprBackend) find(region string) (Workspace, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.ws {
		if b.Region(w) == region {
			return w, true
		}
	}
	return Workspace{}, false
}

// monitorFocused reports whether name is the monitor with focus, from the
// cached list: the focused workspace names its monitor.
func (b *hyprBackend) monitorFocused(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.ws {
		if w.Active {
			return w.Monitor == name
		}
	}
	return false
}
