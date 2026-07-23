// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package wifi

import (
	"fmt"
	"time"

	nm "github.com/Wifx/gonetworkmanager/v3"
	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/module"
)

type wifiModule struct {
	opts *Options

	nmgr       nm.NetworkManager
	devicePath dbus.ObjectPath

	ssid      string
	iface     string
	ap        nm.AccessPoint
	strength  int
	connected bool

	ticker *module.Ticker
}

func (m *wifiModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)

	nmgr, err := nm.NewNetworkManager()
	if err != nil {
		return err
	}
	m.nmgr = nmgr
	m.devicePath = dbus.ObjectPath(
		fmt.Sprintf("/org/freedesktop/NetworkManager/Devices/%d", m.opts.DeviceIndex))

	if err := m.refreshConnection(); err != nil {
		return err
	}
	m.refreshStrength(ctx)

	sigs := nmgr.Subscribe()
	module.On(ctx, module.Chan(sigs), func(sig *dbus.Signal) {
		if m.handleSignal(sig) {
			m.refreshStrength(ctx)
		}
	})

	m.ticker = module.NewTicker(m.opts.Tick.Go())
	module.On(ctx, m.ticker.Source(), func(time.Time) { m.refreshStrength(ctx) })
	return nil
}

func (m *wifiModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
	m.ticker.Set(m.opts.Tick.Go())
}

func (m *wifiModule) Stop(ctx *module.Ctx) {
	if m.nmgr != nil {
		m.nmgr.Unsubscribe()
	}
}

// handleSignal reacts to ActiveAccessPoint changes on the watched device;
// returns true when the connection state may have changed.
func (m *wifiModule) handleSignal(sig *dbus.Signal) bool {
	if sig.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" || sig.Path != m.devicePath {
		return false
	}
	if len(sig.Body) < 2 {
		return false
	}
	iface, _ := sig.Body[0].(string)
	changes, _ := sig.Body[1].(map[string]dbus.Variant)
	if iface != nm.DeviceWirelessInterface || changes == nil {
		return false
	}
	v, ok := changes["ActiveAccessPoint"]
	if !ok {
		return false
	}
	if newPath, ok := v.Value().(dbus.ObjectPath); ok && newPath == "/" {
		m.ssid, m.ap = "", nil
		return true
	}
	_ = m.refreshConnection()
	return true
}

func (m *wifiModule) refreshConnection() error {
	wdev, err := nm.NewDeviceWireless(m.devicePath)
	if err != nil {
		return err
	}
	iface, err := wdev.GetPropertyInterface()
	if err != nil {
		return err
	}
	ap, err := wdev.GetPropertyActiveAccessPoint()
	if err != nil {
		return err
	}

	ssid := ""
	if ap.GetPath() != "/" {
		if ssid, err = ap.GetPropertySSID(); err != nil {
			return err
		}
	}
	m.ap, m.ssid, m.iface = ap, ssid, iface
	return nil
}

func (m *wifiModule) refreshStrength(ctx *module.Ctx) {
	m.connected = m.ssid != ""
	ctx.SetState("disconnected", !m.connected)
	if !m.connected || m.ap == nil || m.ap.GetPath() == "/" {
		m.strength = 0
		return
	}
	if s, err := m.ap.GetPropertyStrength(); err == nil {
		m.strength = int(s)
	}
}

func (m *wifiModule) Render(w *module.Writer) {
	icon := ""
	if len(m.opts.Icons) > 0 {
		idx := utils.Clamp(m.strength*len(m.opts.Icons)/100, 0, len(m.opts.Icons)-1)
		icon = m.opts.Icons[idx]
	}
	w.Text(module.P{
		"icon":      icon,
		"ssid":      m.ssid,
		"interface": m.iface,
		"strength":  m.strength,
	})
}
