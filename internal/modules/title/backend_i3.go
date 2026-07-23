// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package title

import (
	"sync"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/services/i3"
)

type i3Backend struct {
	svc  *i3.Service
	ev   chan interface{}
	ev2  chan interface{}
	done chan struct{}
	sig  chan struct{}

	mu       sync.RWMutex
	instance string
	title    string
}

func newI3Backend(s *i3.Service) backend {
	b := &i3Backend{
		svc:  s,
		ev:   make(chan interface{}, 32),
		ev2:  make(chan interface{}, 32),
		done: make(chan struct{}),
		sig:  make(chan struct{}, 2),
	}

	b.refresh()

	b.svc.RegisterChannel("activeWindow", b.ev)
	b.svc.RegisterChannel("workspaces", b.ev2)

	go b.loop()
	return b
}

func (b *i3Backend) refresh() {
	instance, title := i3.GetTitleClass()
	b.mu.Lock()
	b.instance, b.title = instance, title
	b.mu.Unlock()
}

func (b *i3Backend) Close() {
	close(b.done)
}

func (b *i3Backend) loop() {
	defer logging.Recover("title.i3.loop")
	for {
		select {
		case <-b.done:
			return
		case e := <-b.ev:
			if _, ok := e.(i3.I3WEvent); ok {
				b.refresh()
				b.signal()
			} else {
				logging.Log.Debug().Msgf("title: i3: unknown event on window event channel: %v", e)
			}
		case e := <-b.ev2:
			if _, ok := e.(i3.I3Event); ok {
				b.refresh()
				b.signal()
			} else {
				logging.Log.Debug().Msgf("title: i3: unknown event type on workspace event channel: %v", e)
			}
		}
	}
}

func (b *i3Backend) signal() {
	select {
	case b.sig <- struct{}{}:
	default:
	}
}

func (b *i3Backend) Window() Window {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Window{Title: b.title, Class: b.instance}
}
func (b *i3Backend) Events() <-chan struct{} { return b.sig }
