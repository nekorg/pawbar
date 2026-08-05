// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ws

import (
	"sort"
	"sync"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services/i3"
)

type i3Backend struct {
	svc  *i3.Service
	ev   chan interface{}
	done chan struct{}
	sig  chan struct{}

	mu sync.RWMutex
	ws []Workspace
}

func newI3Backend(s *i3.Service) backend {
	b := &i3Backend{
		svc:  s,
		ev:   make(chan interface{}, 32),
		done: make(chan struct{}),
		sig:  make(chan struct{}, 1),
	}

	b.refresh()

	b.svc.RegisterChannel("workspaces", b.ev)

	go b.loop()
	return b
}

func (b *i3Backend) Close() {
	b.svc.UnregisterChannel(b.ev)
	close(b.done)
}

func (b *i3Backend) loop() {
	defer logging.Recover("ws.i3.loop")
	for {
		select {
		case <-b.done:
			return
		case e := <-b.ev:
			if evt, ok := e.(i3.I3Event); ok {
				logging.Log.Debug().Msgf("ws: i3: event type: %v", evt)
				b.refresh()
				b.signal()
			} else {
				logging.Log.Debug().Msgf("ws: i3: unknown event type: %v", e)
			}
		}
	}
}

// refresh rebuilds the workspace list. GET_WORKSPACES already answers
// every question the bar has: which monitor a workspace is on, whether it
// is on screen there, and which one has focus.
func (b *i3Backend) refresh() {
	workspaces, err := i3.GetWorkspaces()
	if err != nil {
		logging.Log.Warn().Msgf("ws: i3: workspaces query: %v; keeping cached list", err)
		return
	}

	list := make([]Workspace, 0, len(workspaces))
	for _, w := range workspaces {
		list = append(list, Workspace{
			ID:      w.Id,
			Name:    w.Name,
			Monitor: w.Output,
			Active:  w.Focused,
			Visible: w.Visible,
			Urgent:  w.Urgent,
		})
	}
	sortI3(list)

	b.mu.Lock()
	b.ws = list
	b.mu.Unlock()
}

// sortI3 orders workspaces the way i3bar does: numbered ones ascending,
// then named ones alphabetically. Named workspaces all report num -1, so
// they cannot be told apart by number.
func sortI3(list []Workspace) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch {
		case a.ID < 0 && b.ID < 0:
			return a.Name < b.Name
		case a.ID < 0:
			return false
		case b.ID < 0:
			return true
		case a.ID != b.ID:
			return a.ID < b.ID
		default:
			return a.Name < b.Name
		}
	})
}

func (b *i3Backend) signal() {
	select {
	case b.sig <- struct{}{}:
	default:
	}
}

func (b *i3Backend) List() []Workspace {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Workspace(nil), b.ws...)
}

func (b *i3Backend) Events() <-chan struct{} { return b.sig }

// Region identifies a workspace in a click. i3/sway workspace names are
// unique, and they are what the workspace command takes.
func (b *i3Backend) Region(w Workspace) string { return w.Name }

// Goto switches to the clicked workspace, focusing its output first when
// that is a different one, so clicking the bar on the second screen moves
// the user there even for an empty workspace.
func (b *i3Backend) Goto(region string) {
	if target, ok := b.find(region); ok && target.Monitor != "" && !b.outputFocused(target.Monitor) {
		if err := i3.FocusOutput(target.Monitor); err != nil {
			logging.Log.Warn().Msgf("ws: i3: focus output %q: %v", target.Monitor, err)
		}
	}
	i3.GoToWorkspace(region)
}

func (b *i3Backend) find(region string) (Workspace, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.ws {
		if w.Name == region {
			return w, true
		}
	}
	return Workspace{}, false
}

// outputFocused reports whether name is the output with focus: the
// focused workspace names it.
func (b *i3Backend) outputFocused(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.ws {
		if w.Active {
			return w.Monitor == name
		}
	}
	return false
}
