// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package pulse

import (
	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/pkg/module"
)

// Acquire returns the shared refcounted pulse service, starting it on
// first use. Call release when done (module Stop hook).
func Acquire() (*PulseService, func(), error) {
	return services.Acquire("pulse", func() (*PulseService, error) {
		p := &PulseService{}
		if err := p.Start(); err != nil {
			return nil, err
		}
		return p, nil
	})
}

// Sinks is a typed source of state snapshots from an acquired service.
// Each subscription issues its own listener and detaches it on stop, so
// hot-reloaded modules don't leave dead channels behind.
func (p *PulseService) Sinks() module.Source[State] {
	return module.NewSource(func(emit func(State)) (module.Conn, error) {
		l := p.IssueListener()
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
		emit(p.State())
		stop := func() {
			p.RemoveListener(l)
			close(done)
		}
		wake := func() {
			// Resync after suspend: volume may have changed while asleep.
			p.Resync()
			emit(p.State())
		}
		return module.ConnFuncs{StopFn: stop, WakeFn: wake}, nil
	})
}
