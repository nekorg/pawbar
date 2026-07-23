// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package bluetooth

import (
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/pkg/module"
)

type bluetoothModule struct {
	conn      *dbus.Conn
	device    string
	connected bool
	powered   bool
}

func (m *bluetoothModule) Init(ctx *module.Ctx) error {
	// Private connection: Stop closes it, and closing the shared
	// dbus.SystemBus() connection would break every other consumer.
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}
	rule := "type='signal',sender='org.bluez',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'"
	if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule); call.Err != nil {
		return fmt.Errorf("add match rule: %w", call.Err)
	}
	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)
	m.conn = conn

	if err := m.initState(); err != nil {
		return err
	}
	m.applyStates(ctx)

	module.On(ctx, module.Chan(ch), func(sig *dbus.Signal) {
		if err := m.handleSignal(sig); err != nil {
			ctx.Log("%v", err)
			return
		}
		m.applyStates(ctx)
	})
	return nil
}

func (m *bluetoothModule) Stop(ctx *module.Ctx) {
	if m.conn != nil {
		m.conn.Close()
	}
}

func (m *bluetoothModule) applyStates(ctx *module.Ctx) {
	ctx.SetState("off", !m.powered)
	ctx.SetState("disconnected", m.powered && !m.connected)
}

func (m *bluetoothModule) initState() error {
	mgr := m.conn.Object("org.bluez", dbus.ObjectPath("/"))
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := mgr.Call(
		"org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0,
	).Store(&objs); err != nil {
		return fmt.Errorf("GetManagedObjects failed: %w", err)
	}

	var gotAdapter, gotDevice bool
	for _, ifaces := range objs {
		if props, ok := ifaces["org.bluez.Adapter1"]; ok && !gotAdapter {
			if v, exists := props["Powered"]; exists {
				m.powered, _ = v.Value().(bool)
				gotAdapter = true
			}
		}
		if props, ok := ifaces["org.bluez.Device1"]; ok && !gotDevice {
			if v, exists := props["Connected"]; exists {
				m.connected, _ = v.Value().(bool)
			}
			if v, exists := props["Name"]; exists {
				m.device, _ = v.Value().(string)
			}
			gotDevice = true
		}
		if gotAdapter && gotDevice {
			break
		}
	}
	if !gotAdapter {
		return fmt.Errorf("no org.bluez.Adapter1 found in managed objects")
	}
	return nil
}

func (m *bluetoothModule) handleSignal(sig *dbus.Signal) error {
	if len(sig.Body) < 3 {
		return fmt.Errorf("invalid signal")
	}
	iface, ok := sig.Body[0].(string)
	if !ok {
		return fmt.Errorf("invalid interface value")
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return fmt.Errorf("signal body is not a property map")
	}

	switch iface {
	case "org.bluez.Device1":
		if val, exists := changed["Connected"]; exists {
			m.connected, _ = val.Value().(bool)
			obj := m.conn.Object("org.bluez", sig.Path)
			nameVal, err := obj.GetProperty("org.bluez.Device1.Name")
			if err != nil {
				return fmt.Errorf("device name for %s: %w", sig.Path, err)
			}
			m.device, _ = nameVal.Value().(string)
		}
	case "org.bluez.Adapter1":
		if val, exists := changed["Powered"]; exists {
			m.powered, _ = val.Value().(bool)
		}
	}
	return nil
}

func (m *bluetoothModule) Render(w *module.Writer) {
	device := ""
	if m.connected {
		device = m.device
	}
	w.Text(module.P{"device": device})
}
