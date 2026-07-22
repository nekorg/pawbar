// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package clock

import (
	"time"

	"github.com/nekorg/pawbar/internal/menus/calendar"
	"github.com/nekorg/pawbar/pkg/module"
)

type clockModule struct {
	now    time.Time
	ticker *module.Ticker
}

func (m *clockModule) Init(ctx *module.Ctx) error {
	m.now = time.Now()
	m.ticker = module.NewAlignedTicker(m.effectiveTick(ctx))
	module.On(ctx, m.ticker.Source(), func(t time.Time) { m.now = t })

	ctx.HandleVerb("calendar", func(a module.VerbArgs) error {
		x, y := a.XPixel/2, a.YPixel/2
		ctx.Go(func() { calendar.LaunchMenu(x, y) })
		return nil
	})
	return nil
}

// OnState retunes the ticker: a state flip can swap the format, and with
// auto_tick the tick interval follows the displayed granularity.
func (m *clockModule) OnState(ctx *module.Ctx) {
	m.now = time.Now()
	m.ticker.Set(m.effectiveTick(ctx))
}

func (m *clockModule) effectiveTick(ctx *module.Ctx) time.Duration {
	opts := ctx.Options().(*Options)
	if !opts.AutoTick {
		if d := opts.Tick.Go(); d > 0 {
			return d
		}
		return time.Second
	}
	if f := ctx.ActiveBlock().Format; f != nil {
		if g := f.TimeGranularity(); g > 0 {
			return g
		}
	}
	return time.Second
}

func (m *clockModule) Render(w *module.Writer) {
	w.Text(module.P{"time": m.now})
}
