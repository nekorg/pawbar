// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sni

import (
	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/pkg/module"
)

// Acquire returns the shared refcounted sni service, starting it on first
// use. Call release when done (module Stop hook).
func Acquire() (*Service, func(), error) {
	return services.Acquire("sni", func() (*Service, error) {
		s := &Service{}
		if err := s.Start(); err != nil {
			return nil, err
		}
		return s, nil
	})
}

// Events is a typed source of item events from an acquired service. Each
// subscription issues its own listener and detaches it on stop, so a
// hot-reloaded module doesn't leave a dead channel behind for the service
// to go on broadcasting into.
func (s *Service) Events() module.Source[Event] {
	return module.NewSource(func(emit func(Event)) (module.Conn, error) {
		l := s.IssueListener()
		done := make(chan struct{})
		go func() {
			for {
				select {
				case e, ok := <-l:
					if !ok {
						return
					}
					emit(e)
				case <-done:
					return
				}
			}
		}()
		stop := func() {
			s.RemoveListener(l)
			close(done)
		}
		wake := func() {
			// Items may have come or gone while asleep, and nothing told
			// us. Re-enumerate, then make the consumer re-read the list.
			s.Resync()
			emit(Event{Kind: ItemChanged})
		}
		return module.ConnFuncs{StopFn: stop, WakeFn: wake}, nil
	})
}
