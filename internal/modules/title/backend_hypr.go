// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package title

import (
	"strings"
	"sync"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services/hypr"
)

type hyprBackend struct {
	svc  *hypr.Service
	ev   chan hypr.HyprEvent
	done chan struct{}
	sig  chan struct{}

	mu    sync.RWMutex
	class string
	title string
}

func newHyprBackend(s *hypr.Service) backend {
	b := &hyprBackend{
		svc:  s,
		ev:   make(chan hypr.HyprEvent, 32),
		done: make(chan struct{}),
		sig:  make(chan struct{}, 1),
	}

	b.refresh()

	b.svc.RegisterChannel("activewindow", b.ev)
	b.svc.RegisterChannel(hypr.EventReconnect, b.ev)
	go b.loop()
	return b
}

// refresh queries the focused window over IPC; used at startup and after
// an event-socket reconnect.
func (b *hyprBackend) refresh() {
	activews, err := hypr.GetActiveWorkspace()
	if err != nil {
		logging.Log.Warn().Msgf("title: hypr: active workspace query: %v", err)
		return
	}
	clients, err := hypr.GetClients()
	if err != nil {
		logging.Log.Warn().Msgf("title: hypr: clients query: %v", err)
		return
	}

	class := ""
	for _, c := range clients {
		if c.Address == activews.Lastwindow {
			class = c.Class
		}
	}

	b.mu.Lock()
	b.class, b.title = class, activews.Lastwindowtitle
	b.mu.Unlock()
}

func (b *hyprBackend) Close() {
	b.svc.UnregisterChannel(b.ev)
	close(b.done)
}

func (b *hyprBackend) loop() {
	defer logging.Recover("title.hypr.loop")
	for {
		select {
		case <-b.done:
			return
		case e := <-b.ev:
			if e.Event == hypr.EventReconnect {
				b.refresh()
			} else {
				class, title, _ := strings.Cut(e.Data, ",")
				b.mu.Lock()
				b.class, b.title = class, title
				b.mu.Unlock()
			}
			b.signal()
		}
	}
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
