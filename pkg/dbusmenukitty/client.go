// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package dbusmenukitty adapts com.canonical.dbusmenu trees (SNI tray
// context menus) to pawbar's menu framework: it translates the DBus
// layout into a menus.List, forwards clicks/hovers as dbusmenu events,
// loads submenus on demand, and mirrors live layout updates.
package dbusmenukitty

import (
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus"
	"github.com/nekorg/pawbar/pkg/menus/wire"
	"github.com/nekorg/pawbar/pkg/module"
)

type Layout struct {
	Id         int32
	Properties map[string]dbus.Variant
	Children   []Layout
}

type DBusMenuClient struct {
	conn    *dbus.Conn
	obj     dbus.BusObject
	busname string
	path    string
}

func NewDBusMenuClient(busname, path string) (*DBusMenuClient, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("error connecting to session bus: %w", err)
	}

	obj := conn.Object(busname, dbus.ObjectPath(path))

	return &DBusMenuClient{
		conn:    conn,
		obj:     obj,
		busname: busname,
		path:    path,
	}, nil
}

func (c *DBusMenuClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *DBusMenuClient) GetLayout() (Layout, error) {
	return c.GetLayoutForParent(0)
}

func (c *DBusMenuClient) GetLayoutForParent(parentId int32) (Layout, error) {
	call := c.obj.Call("com.canonical.dbusmenu.GetLayout", 0, parentId, -1, []string{})
	if call.Err != nil {
		return Layout{}, fmt.Errorf("error calling GetLayout: %w", call.Err)
	}

	var revision uint32
	var layout Layout
	err := call.Store(&revision, &layout)
	if err != nil {
		return Layout{}, fmt.Errorf("error storing layout: %w", err)
	}

	return layout, nil
}

func (c *DBusMenuClient) SendEvent(id int32, eventType string, data interface{}) error {
	timestamp := uint32(time.Now().Unix())
	call := c.obj.Call("com.canonical.dbusmenu.Event", 0, id, eventType, dbus.MakeVariant(data), timestamp)
	return call.Err
}

func (c *DBusMenuClient) AboutToShow(id int32) (bool, error) {
	call := c.obj.Call("com.canonical.dbusmenu.AboutToShow", 0, id)
	if call.Err != nil {
		return false, call.Err
	}

	var needUpdate bool
	err := call.Store(&needUpdate)
	return needUpdate, err
}

// Open opens (or toggles) the dbusmenu exported at busname/path near
// the anchor, owned by the invoking module (each bus name toggles
// independently). Non-blocking.
func Open(ctx *module.Ctx, at menus.Anchor, busname, path string) {
	ctx.Go(func() {
		h, err := launch(busname, path, func(l *menus.List) (*menus.ListHandle, error) {
			return menus.OpenListH(ctx, at, l)
		})
		if err != nil {
			logging.Log.Error().Msgf("dbusmenu: opening menu of %s: %v", busname, err)
			return
		}
		if h != nil {
			<-h.Done()
		}
	})
}

// LaunchMenu opens the dbusmenu of the item exported at busname/path,
// without a module owner, and blocks until it closes. Standalone use
// (cmd/dbusmenu); x/y are physical pixels.
func LaunchMenu(busname, path string, x, y int) {
	h, err := launch(busname, path, func(l *menus.List) (*menus.ListHandle, error) {
		return menus.LaunchList(menus.Anchor{XPixel: x, YPixel: y}, l)
	})
	if err != nil {
		logging.Log.Error().Msgf("dbusmenu: opening menu of %s: %v", busname, err)
		return
	}
	if h != nil {
		<-h.Done()
	}
}

// launch fetches the menu layout, opens it through the framework and
// wires live updates. It returns a nil handle when the open toggled an
// existing menu closed.
func launch(busname, path string, open func(*menus.List) (*menus.ListHandle, error)) (*menus.ListHandle, error) {
	client, err := NewDBusMenuClient(busname, path)
	if err != nil {
		return nil, err
	}

	items, err := client.itemsFor(0)
	if err != nil {
		client.Close()
		return nil, err
	}

	h, err := open(&menus.List{Items: items, Key: busname})
	if err != nil || h == nil {
		client.Close()
		return nil, err
	}

	go client.watchSignals(h)
	go func() {
		// Closing the connection also ends the signal watcher.
		<-h.Done()
		client.Close()
	}()
	return h, nil
}

// itemsFor loads one level of the dbusmenu tree as list items whose
// callbacks talk back over DBus.
func (c *DBusMenuClient) itemsFor(parentID int32) ([]menus.Item, error) {
	layout, err := c.GetLayoutForParent(parentID)
	if err != nil {
		return nil, err
	}
	return c.convert(layout), nil
}

func (c *DBusMenuClient) convert(parent Layout) []menus.Item {
	items := make([]menus.Item, 0, len(parent.Children))
	for _, l := range parent.Children {
		id := l.Id
		it := menus.Item{}

		if itemType, ok := prop[string](l.Properties, "type"); ok && itemType == "separator" {
			it.Separator = true
			items = append(items, it)
			continue
		}
		if label, ok := prop[string](l.Properties, "label"); ok {
			it.Label = ParseLabel(label).Display
		}
		if enabled, ok := prop[bool](l.Properties, "enabled"); ok {
			it.Disabled = !enabled
		}
		if iconName, ok := prop[string](l.Properties, "icon-name"); ok {
			it.IconName = iconName
		}
		if iconData, ok := prop[[]byte](l.Properties, "icon-data"); ok {
			it.IconData = iconData
		}
		if toggleType, ok := prop[string](l.Properties, "toggle-type"); ok {
			switch toggleType {
			case "checkmark":
				it.Toggle = wire.ToggleCheck
			case "radio":
				it.Toggle = wire.ToggleRadio
			}
		}
		if toggleState, ok := prop[int32](l.Properties, "toggle-state"); ok {
			it.Checked = toggleState == 1
		}
		if childrenDisplay, ok := prop[string](l.Properties, "children-display"); ok && childrenDisplay == "submenu" {
			it.HasSubmenu = true
			it.LoadSubmenu = func() []menus.Item {
				if _, err := c.AboutToShow(id); err != nil {
					logging.Log.Warn().Msgf("dbusmenu: AboutToShow: %v", err)
				}
				sub, err := c.itemsFor(id)
				if err != nil {
					logging.Log.Warn().Msgf("dbusmenu: loading submenu: %v", err)
					return nil
				}
				return sub
			}
		}

		it.OnClick = func() {
			if err := c.SendEvent(id, "clicked", ""); err != nil {
				logging.Log.Warn().Msgf("dbusmenu: sending clicked event: %v", err)
			}
		}
		it.OnHover = func() {
			if err := c.SendEvent(id, "hovered", ""); err != nil {
				logging.Log.Warn().Msgf("dbusmenu: sending hovered event: %v", err)
			}
		}

		items = append(items, it)
	}
	return items
}

// watchSignals mirrors live dbusmenu changes into the open menu. It
// exits when the connection closes (menu closed).
func (c *DBusMenuClient) watchSignals(h *menus.ListHandle) {
	rule := fmt.Sprintf("type='signal',sender='%s',path='%s',interface='com.canonical.dbusmenu'", c.busname, c.path)
	c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)

	ch := make(chan *dbus.Signal, 10)
	c.conn.Signal(ch)

	for sig := range ch {
		switch sig.Name {
		case "com.canonical.dbusmenu.LayoutUpdated",
			"com.canonical.dbusmenu.ItemsPropertiesUpdated":
			items, err := c.itemsFor(0)
			if err != nil {
				logging.Log.Warn().Msgf("dbusmenu: refreshing layout after signal: %v", err)
				continue
			}
			h.Update(items)
		}
	}
}

// prop reads a typed dbusmenu property; a missing key or an off-spec
// value type (some tray apps send those) yields ok=false instead of a
// panic.
func prop[T any](p map[string]dbus.Variant, key string) (T, bool) {
	var zero T
	v, ok := p[key]
	if !ok {
		return zero, false
	}
	t, ok := v.Value().(T)
	if !ok {
		logging.Log.Warn().Msgf("dbusmenu: property %q has unexpected type %T", key, v.Value())
		return zero, false
	}
	return t, true
}

// Label is a dbusmenu label with its access key extracted.
type Label struct {
	Display     string
	AccessKey   rune
	AccessIndex int
	Found       bool
}

// ParseLabel strips dbusmenu underscore access-key markup ("_File" ->
// "File", "__" -> "_").
func ParseLabel(label string) Label {
	runes := []rune(label)
	n := len(runes)

	var output []rune
	outPos := 0
	var result Label

	for i := 0; i < n; {
		if runes[i] == '_' {
			if i+1 < n && runes[i+1] == '_' {
				output = append(output, '_')
				outPos++
				i += 2
			} else {
				if !result.Found && i+1 < n {
					result.Found = true
					result.AccessKey = runes[i+1]
					result.AccessIndex = outPos
					output = append(output, runes[i+1])
					outPos++
					i += 2
				} else {
					i++
				}
			}
		} else {
			output = append(output, runes[i])
			outPos++
			i++
		}
	}

	result.Display = string(output)
	return result
}
