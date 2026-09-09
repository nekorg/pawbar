// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package volume

import (
	"math"

	"github.com/nekorg/pawbar/internal/menus/sink"
	"github.com/nekorg/pawbar/internal/services/pulse"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/menus"
	"github.com/nekorg/pawbar/pkg/module"
)

type volumeModule struct {
	svc     *pulse.PulseService
	release func()

	opts *Options
	st   pulse.State
}

func (m *volumeModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	svc, release, err := pulse.Acquire()
	if err != nil {
		return err
	}
	m.svc, m.release = svc, release

	module.On(ctx, svc.Sinks(), func(st pulse.State) { m.apply(ctx, st) })

	// every verb only queues work; the service owns the socket, so none
	// of these can block the bar on a server that stopped answering.
	ctx.HandleVerb("toggle-mute", func(module.VerbArgs) error {
		s, _ := m.st.DefaultSink()
		return m.svc.SetMute(!s.Muted)
	})
	ctx.HandleVerb("volume-up", func(module.VerbArgs) error {
		return m.svc.AdjustVolume(m.step())
	})
	ctx.HandleVerb("volume-down", func(module.VerbArgs) error {
		return m.svc.AdjustVolume(-m.step())
	})
	ctx.HandleVerb("sink-menu", func(a module.VerbArgs) error {
		return menus.OpenList(ctx, menus.FromVerb(a),
			sink.Menu(m.st, m.opts.AvailableOnly, m.svc.SetDefaultSink))
	})
	return nil
}

func (m *volumeModule) apply(ctx *module.Ctx, st pulse.State) {
	m.st = st
	s, _ := st.DefaultSink()
	ctx.SetState("muted", s.Muted)
	ctx.SetState("disconnected", !st.Connected)
}

// OnState refreshes the cached options: state flips may re-resolve them.
func (m *volumeModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
}

func (m *volumeModule) step() float64 { return float64(m.opts.Step.Go()) }

func (m *volumeModule) Stop(ctx *module.Ctx) {
	if m.release != nil {
		m.release()
	}
}

func (m *volumeModule) Render(w *module.Writer) {
	s, _ := m.st.DefaultSink()
	vol := int(math.Round(s.Volume))
	w.Text(module.P{"icon": m.icon(vol), "vol": vol, "sink": s.Label})
}

func (m *volumeModule) icon(vol int) string {
	icons := m.opts.Icons
	if len(icons) == 0 {
		return ""
	}
	return icons[utils.Clamp(vol*len(icons)/100, 0, len(icons)-1)]
}
