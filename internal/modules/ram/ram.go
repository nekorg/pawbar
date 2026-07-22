// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ram

import (
	"bufio"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nekorg/pawbar/internal/lookup/units"
	"github.com/nekorg/pawbar/pkg/module"
)

type memStat struct {
	Total     uint64
	Available uint64
	Used      uint64
}

func readMeminfo() (memStat, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memStat{}, err
	}
	defer f.Close()

	var total, avail uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		read := func(prefix string) (uint64, bool) {
			if !strings.HasPrefix(line, prefix) {
				return 0, false
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, false
			}
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return v * 1024, true
		}
		if v, ok := read("MemTotal:"); ok {
			total = v
		}
		if v, ok := read("MemAvailable:"); ok {
			avail = v
		}
		if total != 0 && avail != 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return memStat{}, err
	}
	if total == 0 {
		return memStat{}, errors.New("MemTotal not found in /proc/meminfo")
	}
	return memStat{Total: total, Available: avail, Used: total - avail}, nil
}

type ramModule struct {
	opts   *Options
	ticker *module.Ticker
	stat   memStat
}

func (m *ramModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	m.ticker = module.NewTicker(m.opts.Tick.Go())
	m.sample(ctx)
	module.On(ctx, m.ticker.Source(), func(time.Time) { m.sample(ctx) })
	return nil
}

func (m *ramModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
	m.ticker.Set(m.opts.Tick.Go())
}

func (m *ramModule) sample(ctx *module.Ctx) {
	stat, err := readMeminfo()
	if err != nil {
		ctx.Log("meminfo: %v", err)
		return
	}
	m.stat = stat
	pct := usedPct(stat.Used, stat.Total)
	ctx.SetState("warn", pct >= m.opts.WarnAt.Go() && pct < m.opts.CriticalAt.Go())
	ctx.SetState("critical", pct >= m.opts.CriticalAt.Go())
}

func usedPct(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return int(float64(used) * 100 / float64(total))
}

func (m *ramModule) Render(w *module.Writer) {
	system := units.IEC
	if m.opts.UseSI {
		system = units.SI
	}
	unit := m.opts.Scale.Unit
	if m.opts.Scale.Dynamic || unit.Name == "" {
		unit = units.Choose(m.stat.Total, system)
	}
	pct := usedPct(m.stat.Used, m.stat.Total)
	w.Text(module.P{
		"icon":     m.opts.Icon.Go(),
		"used":     round2(units.Format(m.stat.Used, unit)),
		"free":     round2(units.Format(m.stat.Available, unit)),
		"total":    round2(units.Format(m.stat.Total, unit)),
		"used_pct": pct,
		"free_pct": 100 - pct,
		"unit":     unit.Name,
	})
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
