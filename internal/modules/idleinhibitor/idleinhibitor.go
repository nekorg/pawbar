// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package idleinhibitor

import (
	"bytes"
	"fmt"

	"github.com/godbus/dbus/v5"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
)

const (
	portalBusName    = "org.freedesktop.portal.Desktop"
	portalObjectPath = "/org/freedesktop/portal/desktop"
	ifaceInhibit     = "org.freedesktop.portal.Inhibit"
	ifaceRequest     = "org.freedesktop.portal.Request"
	flagIdle         = 8
)

type Format int

func (f *Format) toggle() { *f ^= 1 }

const (
	FormatIdle Format = iota
	FormatInhibit
)

type IdleModule struct {
	receive     chan bool
	send        chan modules.Event
	format      Format
	bus         *dbus.Conn
	handle      dbus.ObjectPath
	opts        Options
	initialOpts Options
}

func (mod *IdleModule) Dependencies() []string {
	return []string{}
}

func (mod *IdleModule) Name() string {
	return "idleinhibitor"
}

func New() modules.Module {
	return &IdleModule{}
}

func (mod *IdleModule) Channels() (<-chan bool, chan<- modules.Event) {
	return mod.receive, mod.send
}

func (mod *IdleModule) setConnection() error {
	bus, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("Failed to connect to session bus: %v\n", err)
	}
	mod.bus = bus
	return nil
}

func (mod *IdleModule) inhibitIdle() error {
	obj := mod.bus.Object(portalBusName, dbus.ObjectPath(portalObjectPath))

	call := obj.Call(ifaceInhibit+".Inhibit", 0, "", uint32(flagIdle), map[string]dbus.Variant{})
	if call.Err != nil {
		return fmt.Errorf("Inhibit call failed: %v\n", call.Err)
	}

	var handle dbus.ObjectPath
	if err := call.Store(&handle); err != nil {
		return fmt.Errorf("Failed to parse handle: %v\n", err)
	}
	mod.handle = handle
	return nil
}

func (mod *IdleModule) closeRequest() error {
	req := mod.bus.Object(portalBusName, mod.handle)
	closeCall := req.Call(ifaceRequest+".Close", 0)
	if closeCall.Err != nil {
		return fmt.Errorf("Failed to remove inhibition: %v\n", closeCall.Err)
	}
	return nil
}

func (mod *IdleModule) Run() (<-chan bool, chan<- modules.Event, error) {
	mod.receive = make(chan bool)
	mod.send = make(chan modules.Event)
	mod.initialOpts = mod.opts
	err := mod.setConnection()
	if err != nil {
		return nil, nil, err
	}
	go func() {
		for {
			select {
			case e := <-mod.send:
				switch ev := e.VaxisEvent.(type) {
				case vaxis.Mouse:
					if ev.EventType != vaxis.EventPress {
						break
					}
					btn := config.ButtonName(ev)

					if btn == "left" {
						mod.format.toggle()
						mod.stateFunc()
						mod.receive <- true
					}
					if mod.opts.OnClick.Dispatch(btn, &mod.initialOpts, &mod.opts) {
						mod.receive <- true
					}

				case modules.FocusIn:
					if mod.opts.OnClick.HoverIn(&mod.opts) {
						mod.receive <- true
					}

				case modules.FocusOut:
					if mod.opts.OnClick.HoverOut(&mod.opts) {
						mod.receive <- true
					}
				}
			}
		}
	}()

	return mod.receive, mod.send, nil
}

func (mod *IdleModule) stateFunc() error {
	switch mod.format {
	case FormatInhibit:
		err := mod.inhibitIdle()
		if err != nil {
			return err
		}
	case FormatIdle:
		err := mod.closeRequest()
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("invalid state caught")
}

func (mod *IdleModule) Render() []modules.EventCell {
	style := vaxis.Style{
		Foreground: mod.opts.Fg.Go(),
		Background: mod.opts.Bg.Go(),
	}
	var tlp config.Format
	switch mod.format {
	case FormatIdle:
		tlp = mod.opts.Format
	case FormatInhibit:
		tlp = mod.opts.Inhibit.Format
		style.Foreground = mod.opts.Inhibit.Fg.Go()
		style.Background = mod.opts.Inhibit.Bg.Go()

	}
	var buf bytes.Buffer
	if err := tlp.Execute(&buf, nil); err != nil {
		return nil
	}

	chars := vaxis.Characters(buf.String())
	r := make([]modules.EventCell, len(chars))
	for i, ch := range chars {
		r[i] = modules.EventCell{
			C: vaxis.Cell{
				Character: ch,
				Style:     style,
			},
			Mod:        mod,
			MouseShape: mod.opts.Cursor.Go(),
		}
	}
	return r
}
