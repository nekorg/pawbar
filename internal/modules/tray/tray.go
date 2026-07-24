// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tray

import (
	_ "embed"
	"image"
	"image/color"
	"strconv"

	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/internal/services/sni"
	"github.com/nekorg/pawbar/pkg/dbusmenukitty"
	"github.com/nekorg/pawbar/pkg/menus"
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
	ctx     *module.Ctx
	items   []sni.Item
	// icons caches decoded images by content key across renders, so Render
	// neither re-decodes nor hands the runtime a fresh pointer for an
	// unchanged icon (which would defeat snapshot de-duplication). A cached
	// nil marks an icon that failed to decode, so it falls back to text.
	icons map[string]image.Image
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
	m.ctx = ctx
	m.icons = make(map[string]image.Image)

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
		if err := m.svc.Activate(item, x, y); err != nil {
			ctx.Log("activate: %v", err)
		}
	case "middle":
		if err := m.svc.SecondaryActivate(item, x, y); err != nil {
			ctx.Log("secondary activate: %v", err)
		}
	case "right":
		if item.MenuPath != "" {
			dbusmenukitty.Open(ctx, menus.FromMouse(ev), item.BusName, string(item.MenuPath))
		} else if err := m.svc.ContextMenu(item, x, y); err != nil {
			ctx.Log("context menu: %v", err)
		}
	case "scroll-up":
		if err := m.svc.Scroll(item, +120, "vertical"); err != nil {
			ctx.Log("scroll: %v", err)
		}
	case "scroll-down":
		if err := m.svc.Scroll(item, -120, "vertical"); err != nil {
			ctx.Log("scroll: %v", err)
		}
	}
}

func (m *trayModule) Render(w *module.Writer) {
	fg := iconColor(m.ctx)
	live := make(map[string]struct{}, len(m.items))
	for i, it := range m.items {
		if i != 0 {
			w.Raw(" ")
		}
		region := module.Region(strconv.Itoa(i))
		img, key := m.icon(it, fg)
		if key != "" {
			live[key] = struct{}{}
		}
		if img != nil {
			w.Icon(img, key, iconCells, region)
		} else {
			w.Raw(labelFor(it), region)
		}
	}
	// Drop decoded icons no longer referenced by any current item.
	for key := range m.icons {
		if _, ok := live[key]; !ok {
			delete(m.icons, key)
		}
	}
}

// icon returns the decoded image and cache key for an item, decoding on a
// cache miss. A cached nil (decode failed / no icon) returns nil so the
// caller falls back to a text label.
func (m *trayModule) icon(it sni.Item, fg color.Color) (image.Image, string) {
	key := iconKey(it, fg)
	if key == "" {
		return nil, ""
	}
	if img, ok := m.icons[key]; ok {
		return img, key
	}
	img := decodeIcon(it, fg)
	m.icons[key] = img
	return img, key
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
