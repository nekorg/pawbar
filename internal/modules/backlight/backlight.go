// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	"fmt"
	"strconv"

	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/monitor"
	"github.com/nekorg/pawbar/internal/services/ddc"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/module"
)

// monitorSelf means "the output this bar is pinned to".
const monitorSelf = "self"

// backend is one way of reading and writing a display's brightness. The two
// implementations differ enormously in cost — a sysfs read is a file read, a
// DDC write is 50ms on an I2C bus — which is why Set is allowed to return
// before the change has physically happened.
type backend interface {
	// Name is what {backend} renders.
	Name() string

	// Start subscribes to brightness changes.
	Start(ctx *module.Ctx) error

	// Pct is the current brightness, 0-100.
	Pct() int

	// Raw is the backend's own value and its maximum.
	Raw() (now, max int)

	// Set requests a new percentage.
	Set(pct int) error

	Stop()
}

type backlightModule struct {
	opts *Options

	// b is the active backend: what Render, Set and the verbs go through.
	b backend

	// ddc is the DDC/CI backend when one was planned. It stays subscribed
	// even while sysfs is standing in for it, which is what lets a display
	// that was not answering yet take over later.
	ddc *ddcBackend

	// sys is the sysfs stand-in, built on the first demotion and kept.
	sys *sysfsBackend
}

func (m *backlightModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)

	b, err := m.pick(ctx)
	if err != nil {
		return err
	}
	m.b = b
	if err := b.Start(ctx); err != nil {
		return err
	}

	ctx.HandleVerb("brightness-up", func(module.VerbArgs) error {
		return m.step(+1)
	})
	ctx.HandleVerb("brightness-down", func(module.VerbArgs) error {
		return m.step(-1)
	})
	ctx.HandleVerb("set-brightness", func(a module.VerbArgs) error {
		if len(a.Args) == 0 {
			return fmt.Errorf("set-brightness needs a percentage")
		}
		pct, err := strconv.Atoi(a.Args[0])
		if err != nil {
			return fmt.Errorf("set-brightness: %q is not a percentage", a.Args[0])
		}
		return m.b.Set(utils.Clamp(pct, 0, 100))
	})
	return nil
}

// pick resolves which backend controls this bar's monitor.
func (m *backlightModule) pick(ctx *module.Ctx) (backend, error) {
	want := m.opts.Monitor
	if want == monitorSelf {
		want = monitor.Self()
	}

	cs, err := monitor.Connectors()
	if err != nil {
		ctx.Log("read DRM connectors: %v", err)
	}
	devs := attribute(scanSysfs(), cs)

	// An unpinned bar has no output to speak for. Geometry can be guessed
	// from the primary monitor; a brightness write cannot, because it
	// changes hardware the user never named. So auto stays on sysfs.
	if want == "" && m.opts.Backend == ModeDDC {
		if info, ok := monitor.Info(); ok {
			want = info.Name
			logging.Log.Warn().Msgf(
				"backlight: no pinned output; assuming %s for the ddc backend", want)
		}
	}

	p, err := resolve(m.opts.Backend, want, devs, cs, ddc.ServiceAvailable())
	if err != nil {
		return nil, err
	}

	logging.Log.Info().Msgf("backlight: %s", describe(p))

	if p.Mode == ModeDDC {
		b := newDDCBackend(p.Display, m.opts.Poll.Go())
		m.ddc = b
		// Under `auto` a display that is not answering is not fatal: fall
		// back to whatever sysfs device exists rather than showing an
		// error chip for a monitor that simply lacks DDC/CI. The retreat
		// is not final — a monitor still waking up at login answers a
		// minute later, and promote takes it from there.
		if m.opts.Backend == ModeAuto {
			b.onFail = func(cause error) { m.demote(ctx, devs, cause) }
			b.onReady = func() { m.promote() }
		}
		return b, nil
	}
	m.sys = newSysfsBackend(p.Device)
	return m.sys, nil
}

// demote stands a sysfs backend in for a DDC display that is not answering.
func (m *backlightModule) demote(ctx *module.Ctx, devs []sysfsDevice, cause error) {
	if m.ddc == nil || m.b != m.ddc {
		return
	}
	if m.sys == nil {
		d, ok := legacyDevice(devs)
		if !ok {
			return
		}
		b := newSysfsBackend(d)
		if err := b.Start(ctx); err != nil {
			ctx.Log("sysfs fallback: %v", err)
			return
		}
		m.sys = b
	}
	logging.Log.Info().Msgf("backlight: ddc unavailable (%v); falling back to %s", cause, m.sys.dev.Name)

	// The DDC backend is deliberately left running. Stopping it releases
	// the ddc service handle, which drops the display's worker and with it
	// every chance of ever probing again — that is precisely what made a
	// monitor that was merely slow to wake at login stay on the wrong
	// device until pawbar was restarted.
	m.b = m.sys
}

// promote hands control back to the DDC display once it starts answering.
func (m *backlightModule) promote() {
	if m.ddc == nil || m.b == m.ddc {
		return
	}
	logging.Log.Info().Msgf("backlight: %s: ddc/ci is answering; taking over from %s",
		m.ddc.display.Connector, m.sys.dev.Name)

	// m.sys keeps running: its udev subscription costs nothing, and a
	// display that drops out again then needs no restart.
	m.b = m.ddc
}

func describe(p plan) string {
	switch p.Mode {
	case ModeDDC:
		return fmt.Sprintf("%s via ddc/ci (i2c bus %d)", p.Connector, p.Display.I2CBus)
	default:
		if p.Connector != "" {
			return fmt.Sprintf("%s via sysfs (%s)", p.Connector, p.Device.Name)
		}
		return fmt.Sprintf("via sysfs (%s)", p.Device.Name)
	}
}

func (m *backlightModule) step(dir int) error {
	step := m.opts.Step.Go()
	return m.b.Set(utils.Clamp(m.b.Pct()+dir*step, 0, 100))
}

// OnState refreshes the cached options: state flips may re-resolve them.
// Only the presentation options are read live — the backend, its output and
// its poll interval are settled at Init, because tearing an I2C worker down
// on a state flip would cost far more than it could ever be worth.
func (m *backlightModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
}

// Stop tears down both backends: after a demotion the sysfs stand-in and the
// DDC backend are live at once, and only one of them is the active `b`.
func (m *backlightModule) Stop(ctx *module.Ctx) {
	if m.ddc != nil {
		m.ddc.Stop()
	}
	if m.sys != nil {
		m.sys.Stop()
	}
}

func (m *backlightModule) Render(w *module.Writer) {
	now, max := m.b.Raw()
	if max == 0 {
		return
	}
	pct := m.b.Pct()
	icon := ""
	if len(m.opts.Icons) > 0 {
		icon = m.opts.Icons[utils.Clamp(pct*len(m.opts.Icons)/100, 0, len(m.opts.Icons)-1)]
	}
	w.Text(module.P{
		"icon": icon, "light": pct, "now": now, "max": max,
		"backend": m.b.Name(),
	})
}
