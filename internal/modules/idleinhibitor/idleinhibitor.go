// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package idleinhibitor

import (
	_ "embed"
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed idleinhibitor.yaml
var defaults []byte

func init() {
	module.Register(module.Def{
		Name: "idleinhibitor",
		Doc:  "keep the system awake via the desktop portal",
		New:  func() module.Module { return &idleModule{} },
		States: []module.StateDef{
			{Name: "inhibiting", Doc: "idle is currently inhibited"},
		},
		Verbs: []module.VerbDef{
			{Name: "toggle", Doc: "toggle idle inhibition"},
		},
		Defaults: defaults,
	})
}

const (
	portalBusName    = "org.freedesktop.portal.Desktop"
	portalObjectPath = "/org/freedesktop/portal/desktop"
	ifaceInhibit     = "org.freedesktop.portal.Inhibit"
	ifaceRequest     = "org.freedesktop.portal.Request"
	flagIdle         = 8
)

type idleModule struct {
	bus        *dbus.Conn
	handle     dbus.ObjectPath
	inhibiting bool
}

func (m *idleModule) Init(ctx *module.Ctx) error {
	bus, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("session bus: %w", err)
	}
	m.bus = bus

	ctx.HandleVerb("toggle", func(module.VerbArgs) error {
		if m.inhibiting {
			if err := m.closeRequest(); err != nil {
				return err
			}
		} else {
			if err := m.inhibitIdle(); err != nil {
				return err
			}
		}
		m.inhibiting = !m.inhibiting
		ctx.SetState("inhibiting", m.inhibiting)
		return nil
	})
	return nil
}

func (m *idleModule) Stop(ctx *module.Ctx) {
	if m.inhibiting {
		_ = m.closeRequest()
	}
}

func (m *idleModule) inhibitIdle() error {
	obj := m.bus.Object(portalBusName, dbus.ObjectPath(portalObjectPath))
	call := obj.Call(ifaceInhibit+".Inhibit", 0, "", uint32(flagIdle), map[string]dbus.Variant{})
	if call.Err != nil {
		return fmt.Errorf("Inhibit call failed: %w", call.Err)
	}
	var handle dbus.ObjectPath
	if err := call.Store(&handle); err != nil {
		return fmt.Errorf("parse inhibit handle: %w", err)
	}
	m.handle = handle
	return nil
}

func (m *idleModule) closeRequest() error {
	req := m.bus.Object(portalBusName, m.handle)
	if call := req.Call(ifaceRequest+".Close", 0); call.Err != nil {
		return fmt.Errorf("remove inhibition: %w", call.Err)
	}
	return nil
}

func (m *idleModule) Render(w *module.Writer) {
	w.Text(module.P{})
}
