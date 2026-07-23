// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tray

import (
	_ "embed"
	"strconv"

	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/internal/services/sni"
	"github.com/nekorg/pawbar/pkg/dbusmenukitty"
	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed tray.yaml
var defaults []byte

// Left click activates an item, middle click secondary-activates, right
// click opens its menu, scrolling scrolls it.
func init() {
	module.Register(module.Def{
		Name:     "tray",
		Doc:      "status notifier item tray",
		New:      func() module.Module { return &trayModule{} },
		Defaults: defaults,
	})
}

type trayModule struct {
	svc     *sni.Service
	release func()
	items   []sni.Item
}

func (m *trayModule) Init(ctx *module.Ctx) error {
	svc, release, err := services.Acquire("sni", func() (*sni.Service, error) {
		s := &sni.Service{}
		if err := s.Start(); err != nil {
			return nil, err
		}
		return s, nil
	})
	if err != nil {
		return err
	}
	m.svc, m.release = svc, release

	m.items = svc.Items()
	module.On(ctx, module.Chan(svc.IssueListener()), func(sni.Event) {
		m.items = m.svc.Items()
	})
	return nil
}

func (m *trayModule) Stop(ctx *module.Ctx) {
	if m.release != nil {
		m.release()
	}
}

func (m *trayModule) OnMouse(ctx *module.Ctx, ev module.Mouse) {
	if ev.Kind != "press" {
		return
	}
	idx, err := strconv.Atoi(ev.Region)
	if err != nil || idx < 0 || idx >= len(m.items) {
		return
	}
	item := m.items[idx]
	x, y := int32(ev.XPixel), int32(ev.YPixel)

	switch ev.Button {
	case "left":
		_ = m.svc.Activate(item, x, y)
	case "middle":
		_ = m.svc.SecondaryActivate(item, x, y)
	case "right":
		if item.MenuPath != "" {
			// TODO: this assumes 2x scale, use pkg/monitor to determine
			// the correct scale.
			px, py := ev.XPixel/2, ev.YPixel/2
			busname, menupath := item.BusName, string(item.MenuPath)
			ctx.Go(func() { dbusmenukitty.LaunchMenu(busname, menupath, px, py) })
		} else if err := m.svc.ContextMenu(item, x, y); err != nil {
			ctx.Log("context menu: %v", err)
		}
	case "scroll-up":
		_ = m.svc.Scroll(item, +120, "vertical")
	case "scroll-down":
		_ = m.svc.Scroll(item, -120, "vertical")
	}
}

func (m *trayModule) Render(w *module.Writer) {
	for i, it := range m.items {
		if i != 0 {
			w.Raw(" ")
		}
		w.Raw(labelFor(it), module.Region(strconv.Itoa(i)))
	}
}

func labelFor(it sni.Item) string {
	switch {
	case it.IconName != "":
		return it.IconName
	case it.Title != "":
		return it.Title
	case it.Id != "":
		return it.Id
	default:
		return "?"
	}
}
