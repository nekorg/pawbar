// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package volume

import (
	"math"
	"strings"

	"github.com/nekorg/pawbar/internal/logging"
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
		// options are only readable here, on the module goroutine.
		only := m.opts.AvailableOnly
		at := menus.FromVerb(a)
		ctx.Go(func() { liveSinkMenu(ctx, at, m.svc, only) })
		return nil
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

// liveSinkMenu keeps the menu in step with the server for as long as it
// is open: volumes move, devices come and go, a monitor gets plugged in.
// It runs off the module goroutine, so it takes the service directly
// rather than reading module state.
func liveSinkMenu(ctx *module.Ctx, at menus.Anchor, svc *pulse.PulseService, only bool) {
	set := svc.SetDefaultSink
	items := sink.Items(svc.State(), only, set)

	h, err := menus.OpenListH(ctx, at, &menus.List{Items: items})
	if err != nil {
		logging.Log.Error().Msgf("volume: opening sink menu: %v", err)
		return
	}
	if h == nil {
		return // the click toggled an open menu closed
	}

	l := svc.IssueListener()
	defer svc.RemoveListener(l)

	shown := itemSig(items)
	for {
		select {
		case <-h.Done():
			return
		case st := <-l:
			next := sink.Items(st, only, set)
			// most snapshots change nothing the menu shows; resending
			// them would respawn ids and resize the panel for nothing.
			if sig := itemSig(next); sig != shown {
				shown = sig
				h.Update(next)
			}
		}
	}
}

func itemSig(items []menus.Item) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Label)
		if it.Checked {
			b.WriteByte(1)
		}
		b.WriteByte(0)
	}
	return b.String()
}

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
