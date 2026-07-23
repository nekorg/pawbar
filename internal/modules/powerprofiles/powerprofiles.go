// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package powerprofiles

import (
	_ "embed"
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/menus/power"
	"github.com/nekorg/pawbar/internal/scale"
	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed powerprofiles.yaml
var defaults []byte

func init() {
	module.Register(module.Def{
		Name: "powerprofiles",
		Doc:  "power profile via upower power-profiles-daemon",
		New:  func() module.Module { return &ppModule{} },
		States: []module.StateDef{
			{Name: "performance", Doc: "performance profile active"},
			{Name: "balanced", Doc: "balanced profile active"},
			{Name: "power-saver", Doc: "power-saver profile active"},
		},
		Placeholders: []module.Placeholder{
			{Name: "profile", Doc: "active profile name", Kind: module.KindString},
		},
		Verbs: []module.VerbDef{
			{Name: "toggle", Doc: "cycle performance, power-saver, balanced"},
			{Name: "menu", Doc: "open the power profile menu at the pointer"},
		},
		Defaults: defaults,
	})
}

const (
	ppIface = "org.freedesktop.UPower.PowerProfiles"
	ppPath  = "/org/freedesktop/UPower/PowerProfiles"
)

type ppModule struct {
	conn    *dbus.Conn
	obj     dbus.BusObject
	profile string
}

func (m *ppModule) Init(ctx *module.Ctx) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}
	m.conn = conn
	m.obj = conn.Object(ppIface, ppPath)

	rule := "type='signal',sender='" + ppIface + "',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'"
	if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule); call.Err != nil {
		conn.Close()
		return fmt.Errorf("add match rule: %w", call.Err)
	}
	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)

	profile, err := m.getProfile()
	if err != nil {
		conn.Close()
		return err
	}
	m.setProfileState(ctx, profile)

	module.On(ctx, module.Chan(ch), func(sig *dbus.Signal) {
		if len(sig.Body) < 2 {
			return
		}
		iface, _ := sig.Body[0].(string)
		changed, ok := sig.Body[1].(map[string]dbus.Variant)
		if iface != ppIface || !ok {
			return
		}
		if v, ok := changed["ActiveProfile"]; ok {
			if s, ok := v.Value().(string); ok {
				m.setProfileState(ctx, s)
			}
		}
	})

	ctx.HandleVerb("toggle", func(module.VerbArgs) error {
		switch m.profile {
		case "performance":
			return m.setProfile("power-saver")
		case "power-saver":
			return m.setProfile("balanced")
		default:
			return m.setProfile("performance")
		}
	})
	ctx.HandleVerb("menu", func(a module.VerbArgs) error {
		x, y := scale.Logical(a.XPixel, a.YPixel)
		ctx.Go(func() { power.LaunchMenu(x, y) })
		return nil
	})
	return nil
}

func (m *ppModule) Stop(ctx *module.Ctx) {
	if m.conn != nil {
		m.conn.Close()
	}
}

func (m *ppModule) setProfileState(ctx *module.Ctx, profile string) {
	m.profile = profile
	for _, s := range []string{"performance", "balanced", "power-saver"} {
		ctx.SetState(s, s == profile)
	}
}

func (m *ppModule) getProfile() (string, error) {
	var v dbus.Variant
	if err := m.obj.Call("org.freedesktop.DBus.Properties.Get", 0,
		ppIface, "ActiveProfile").Store(&v); err != nil {
		return "", fmt.Errorf("get ActiveProfile: %w", err)
	}
	s, _ := v.Value().(string)
	return s, nil
}

func (m *ppModule) setProfile(profile string) error {
	call := m.obj.Call("org.freedesktop.DBus.Properties.Set", 0,
		ppIface, "ActiveProfile", dbus.MakeVariant(profile))
	if call.Err != nil {
		return fmt.Errorf("set ActiveProfile: %w", call.Err)
	}
	return nil
}

func (m *ppModule) Render(w *module.Writer) {
	w.Text(module.P{"profile": m.profile})
}
