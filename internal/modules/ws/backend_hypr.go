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

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services/hypr"
)

type hyprBackend struct {
	svc  *hypr.Service
	ev   chan hypr.HyprEvent
	done chan struct{}
	ws   map[int]*Workspace
	mu   sync.RWMutex
	sig  chan struct{}
}

func newHyprBackend(s *hypr.Service) backend {
	b := &hyprBackend{
		svc:  s,
		ev:   make(chan hypr.HyprEvent, 32),
		done: make(chan struct{}),
		ws:   make(map[int]*Workspace),
		sig:  make(chan struct{}, 1),
	}

	b.refreshWorkspaceCache()

	for _, e := range []string{"workspacev2", "focusedmonv2", "createworkspacev2", "destroyworkspacev2", "activespecial", "renameworkspace", "urgent", hypr.EventReconnect} {
		b.svc.RegisterChannel(e, b.ev)
	}

	go b.loop()
	return b
}

func (b *hyprBackend) Close() {
	b.svc.UnregisterChannel(b.ev)
	close(b.done)
}

func (b *hyprBackend) loop() {
	defer logging.Recover("ws.hypr.loop")
	for {
		select {
		case <-b.done:
			return
		case e := <-b.ev:
			if !b.validate(e) {
				b.refreshWorkspaceCache()
			}

			if b.handleEvent(e) {
				b.signal()
			}
		}
	}
}

func (b *hyprBackend) refreshWorkspaceCache() {
	workspaces, err := hypr.GetWorkspaces()
	if err != nil {
		logging.Log.Warn().Msgf("ws: hypr: workspaces query: %v; keeping cached list", err)
		return
	}
	active, err := hypr.GetActiveWorkspace()
	if err != nil {
		logging.Log.Warn().Msgf("ws: hypr: active workspace query: %v; keeping cached list", err)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.ws = make(map[int]*Workspace)
	for _, w := range workspaces {
		b.ws[w.Id] = &Workspace{
			ID:      w.Id,
			Name:    w.Name,
			Active:  w.Id == active.Id,
			Special: strings.HasPrefix(w.Name, "special:"),
		}
	}
}

func (b *hyprBackend) signal() {
	select {
	case b.sig <- struct{}{}:
	default:
	}
}

// eventID parses the leading "ID," field of a *v2 event payload.
func eventID(data string) (int, bool) {
	idStr, _, found := strings.Cut(data, ",")
	if !found {
		return 0, false
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, false
	}
	return id, true
}

// validate reports whether the event is consistent with the cache; a
// false return triggers a cache refresh before the event is applied.
func (b *hyprBackend) validate(e hypr.HyprEvent) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	switch e.Event {
	case "workspacev2", "destroyworkspacev2", "renameworkspace":
		id, ok := eventID(e.Data)
		if !ok {
			return false
		}
		_, known := b.ws[id]
		return known

	case "focusedmonv2":
		// Workspace names may contain commas; the id is the last field.
		id, err := strconv.Atoi(e.Data[strings.LastIndex(e.Data, ",")+1:])
		if err != nil {
			return false
		}
		_, known := b.ws[id]
		return known

	case "createworkspacev2":
		id, ok := eventID(e.Data)
		if !ok {
			return false
		}
		_, known := b.ws[id]
		return !known

	case hypr.EventReconnect:
		// Events were missed while disconnected; always refresh.
		return false
	}

	return true
}

func (b *hyprBackend) handleEvent(e hypr.HyprEvent) bool {
	// Query clients before taking the lock: IPC must not stall List().
	var clients []hypr.Client
	if e.Event == "urgent" {
		var err error
		clients, err = hypr.GetClients()
		if err != nil {
			logging.Log.Warn().Msgf("ws: hypr: clients query: %v", err)
			return false
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	switch e.Event {
	case "workspacev2":
		id, ok := eventID(e.Data)
		if !ok {
			return false
		}
		b.setActiveWorkspace(id)
	case "createworkspacev2":
		id_str, name, _ := strings.Cut(e.Data, ",")
		id, err := strconv.Atoi(id_str)
		if err != nil {
			return false
		}
		b.createWorkspace(id, name)
	case "destroyworkspacev2":
		id, ok := eventID(e.Data)
		if !ok {
			return false
		}
		b.destroyWorkspace(id)
	case "activespecial":
		name, _, _ := strings.Cut(e.Data, ",")
		b.activateSpecialWorkspace(name)
	case "urgent":
		b.setWorkspaceUrgent(e.Data, clients)
	case "renameworkspace":
		idr, name, _ := strings.Cut(e.Data, ",")
		id, err := strconv.Atoi(idr)
		if err != nil {
			return false
		}
		b.renameWorkspace(id, name)
	case hypr.EventReconnect:
		// Cache already refreshed by validate; just re-render.
	default:
		return false
	}
	return true
}

func (b *hyprBackend) renameWorkspace(id int, name string) {
	if w, ok := b.ws[id]; ok {
		w.Name = name
	}
}

func (b *hyprBackend) setActiveWorkspace(id int) {
	w, ok := b.ws[id]
	if !ok {
		return
	}

	for _, o := range b.ws {
		if !o.Special {
			o.Active = false
		}
	}

	w.Active = true
	w.Urgent = false
}

func (b *hyprBackend) createWorkspace(id int, name string) {
	b.ws[id] = &Workspace{
		ID:      id,
		Name:    name,
		Active:  false,
		Special: strings.HasPrefix(name, "special:"),
	}
}

func (b *hyprBackend) destroyWorkspace(id int) {
	delete(b.ws, id)
}

func (b *hyprBackend) activateSpecialWorkspace(name string) {
	active := name != ""

	for _, w := range b.ws {
		if w.Special {
			w.Active = active
		}
	}
}

func (b *hyprBackend) setWorkspaceUrgent(address string, clients []hypr.Client) {
	activeId := 0
	for _, w := range b.ws {
		if w.Active && !w.Special {
			activeId = w.ID
		}
	}

	for _, client := range clients {
		client_address, _ := strings.CutPrefix(client.Address, "0x")
		if client_address == address && client.Workspace.Id != activeId {
			if w, ok := b.ws[client.Workspace.Id]; ok {
				w.Urgent = true
			}
		}
	}
}

func (b *hyprBackend) List() []Workspace {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ws := make([]Workspace, 0, len(b.ws))
	for _, v := range b.ws {
		ws = append(ws, Workspace{v.ID, v.Name, v.Active, v.Urgent, v.Special})
	}
	sort.Slice(ws, func(a, b int) bool { return ws[a].ID < ws[b].ID })
	return ws
}
func (b *hyprBackend) Events() <-chan struct{} { return b.sig }

func (b *hyprBackend) Goto(name string) {
	if err := hypr.GoToWorkspace(name); err != nil {
		logging.Log.Error().Msgf("ws: hypr: goto %q: %v", name, err)
	}
}
