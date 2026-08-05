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
	b    backend
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
		// Under `auto` a display that stops answering is not fatal: fall
		// back to whatever sysfs device exists rather than showing an
		// error chip for a monitor that simply lacks DDC/CI.
		if m.opts.Backend == ModeAuto {
			b.onFail = func(cause error) { m.demote(ctx, devs, cause) }
		}
		return b, nil
	}
	return newSysfsBackend(p.Device), nil
}

// demote swaps a failed DDC backend for a sysfs one, once.
func (m *backlightModule) demote(ctx *module.Ctx, devs []sysfsDevice, cause error) {
	if _, isDDC := m.b.(*ddcBackend); !isDDC {
		return
	}
	d, ok := legacyDevice(devs)
	if !ok {
		return
	}
	logging.Log.Info().Msgf("backlight: ddc unavailable (%v); falling back to %s", cause, d.Name)

	m.b.Stop()
	b := newSysfsBackend(d)
	m.b = b
	if err := b.Start(ctx); err != nil {
		ctx.Log("sysfs fallback: %v", err)
	}
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

func (m *backlightModule) Stop(ctx *module.Ctx) {
	if m.b != nil {
		m.b.Stop()
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
