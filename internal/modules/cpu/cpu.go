// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cpu

import (
	"time"

	"github.com/nekorg/pawbar/pkg/module"
	"github.com/shirou/gopsutil/v3/cpu"
)

type cpuModule struct {
	opts   *Options
	ticker *module.Ticker

	usage     int
	highStart time.Time
}

func (m *cpuModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	m.ticker = module.NewTicker(m.opts.Tick.Go())
	m.sample(ctx)
	module.On(ctx, m.ticker.Source(), func(time.Time) { m.sample(ctx) })
	return nil
}

func (m *cpuModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
	m.ticker.Set(m.opts.Tick.Go())
}

func (m *cpuModule) sample(ctx *module.Ctx) {
	percent, err := cpu.Percent(0, false)
	if err != nil || len(percent) == 0 {
		return
	}
	m.usage = int(percent[0])

	// high arms only after usage stays above high_at for high_for.
	if m.usage > m.opts.HighAt.Go() {
		if m.highStart.IsZero() {
			m.highStart = time.Now()
		}
		ctx.SetState("high", time.Since(m.highStart) >= m.opts.HighFor.Go())
	} else {
		m.highStart = time.Time{}
		ctx.SetState("high", false)
	}
}

func (m *cpuModule) Render(w *module.Writer) {
	w.Text(module.P{"cpu": m.usage})
}
