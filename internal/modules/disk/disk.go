// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package disk

import (
	"math"
	"time"

	"github.com/nekorg/pawbar/internal/lookup/units"
	"github.com/nekorg/pawbar/pkg/module"
	"github.com/shirou/gopsutil/v3/disk"
)

type diskModule struct {
	opts   *Options
	ticker *module.Ticker

	used, free, total uint64
}

func (m *diskModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	m.ticker = module.NewTicker(m.opts.Tick.Go())
	m.sample(ctx)
	module.On(ctx, m.ticker.Source(), func(time.Time) { m.sample(ctx) })
	return nil
}

func (m *diskModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
	m.ticker.Set(m.opts.Tick.Go())
}

func (m *diskModule) sample(ctx *module.Ctx) {
	du, err := disk.Usage(m.opts.Path)
	if err != nil {
		ctx.Log("usage %s: %v", m.opts.Path, err)
		return
	}
	m.used, m.free, m.total = du.Used, du.Free, du.Total
	pct := int(du.UsedPercent)
	ctx.SetState("warn", pct >= m.opts.WarnAt.Go() && pct < m.opts.CriticalAt.Go())
	ctx.SetState("critical", pct >= m.opts.CriticalAt.Go())
}

func (m *diskModule) Render(w *module.Writer) {
	system := units.IEC
	if m.opts.UseSI {
		system = units.SI
	}
	unit := m.opts.Scale.Unit
	if m.opts.Scale.Dynamic || unit.Name == "" {
		unit = units.Choose(m.total, system)
	}
	pct := 0
	if m.total > 0 {
		pct = int(float64(m.used) * 100 / float64(m.total))
	}
	w.Text(module.P{
		"icon":     m.opts.Icon.Go(),
		"used":     round2(units.Format(m.used, unit)),
		"free":     round2(units.Format(m.free, unit)),
		"total":    round2(units.Format(m.total, unit)),
		"used_pct": pct,
		"free_pct": 100 - pct,
		"unit":     unit.Name,
	})
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
