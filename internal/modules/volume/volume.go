// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package volume

import (
	"math"

	"github.com/nekorg/pawbar/internal/services/pulse"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/module"
)

type volumeModule struct {
	svc     *pulse.PulseService
	release func()

	opts   *Options
	sink   string
	volume float64
	muted  bool
}

func (m *volumeModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	svc, release, err := pulse.Acquire()
	if err != nil {
		return err
	}
	m.svc, m.release = svc, release

	if e, err := svc.GetDefaultSinkInfo(); err == nil {
		m.apply(ctx, e)
	}
	module.On(ctx, svc.Sinks(), func(e pulse.SinkEvent) { m.apply(ctx, e) })

	ctx.HandleVerb("toggle-mute", func(module.VerbArgs) error {
		return m.svc.SetSinkMute(m.sink, !m.muted)
	})
	ctx.HandleVerb("volume-up", func(module.VerbArgs) error {
		return m.changeVolume(+1)
	})
	ctx.HandleVerb("volume-down", func(module.VerbArgs) error {
		return m.changeVolume(-1)
	})
	return nil
}

func (m *volumeModule) apply(ctx *module.Ctx, e pulse.SinkEvent) {
	m.sink = e.Sink
	m.volume = e.Volume
	m.muted = e.Muted
	ctx.SetState("muted", e.Muted)
}

// OnState refreshes the cached options: state flips may re-resolve them.
func (m *volumeModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
}

func (m *volumeModule) changeVolume(dir float64) error {
	step := float64(m.opts.Step.Go())
	v := math.Max(0, math.Min(100, m.volume+dir*step))
	return m.svc.SetSinkVolume(m.sink, v)
}

func (m *volumeModule) Stop(ctx *module.Ctx) {
	if m.release != nil {
		m.release()
	}
}

func (m *volumeModule) Render(w *module.Writer) {
	vol := int(math.Round(m.volume))
	w.Text(module.P{"icon": m.icon(vol), "vol": vol})
}

func (m *volumeModule) icon(vol int) string {
	icons := m.opts.Icons
	if len(icons) == 0 {
		return ""
	}
	return icons[utils.Clamp(vol*len(icons)/100, 0, len(icons)-1)]
}
