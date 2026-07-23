// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package battery

import (
	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/module"
)

type batteryModule struct {
	opts   *Options
	conn   *dbus.Conn
	device UPowerDevice
}

func (m *batteryModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)

	conn, ch, err := ConnectUPower()
	if err != nil {
		return err
	}
	m.conn = conn
	m.device, err = GetDisplayDevice(conn)
	if err != nil {
		ctx.Log("display device query: %v", err)
	}
	m.applyStates(ctx)

	module.On(ctx, module.Chan(ch), func(sig *dbus.Signal) {
		HandleSignal(sig, &m.device)
		m.applyStates(ctx)
	})
	return nil
}

func (m *batteryModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
}

func (m *batteryModule) Stop(ctx *module.Ctx) {
	if m.conn != nil {
		m.conn.Close()
	}
}

func (m *batteryModule) applyStates(ctx *module.Ctx) {
	pct := int(m.device.Percentage)
	ctx.SetState("charging", m.device.State == StateCharging)
	ctx.SetState("charged", m.device.State == StateFullyCharged)
	ctx.SetState("low", pct <= m.opts.LowAt.Go())
	ctx.SetState("warn", pct <= m.opts.WarnAt.Go() && pct > m.opts.LowAt.Go())
}

func (m *batteryModule) Render(w *module.Writer) {
	pct := int(m.device.Percentage)

	var icon string
	var eta int
	switch m.device.State {
	case StateFullyCharged:
		icon = m.opts.ChargedIcon
	case StateCharging:
		icon = pickIcon(m.opts.ChargingIcons, pct)
		eta = int(m.device.TimeToFull)
	default:
		icon = pickIcon(m.opts.DischargingIcons, pct)
		eta = int(m.device.TimeToEmpty)
	}

	w.Text(module.P{
		"icon":    icon,
		"bat":     pct,
		"hours":   eta / 3600,
		"minutes": (eta / 60) % 60,
	})
}

func pickIcon(icons []string, pct int) string {
	if len(icons) == 0 {
		return ""
	}
	return icons[utils.Clamp(pct*len(icons)/100, 0, len(icons)-1)]
}
