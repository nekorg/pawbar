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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus"
	"github.com/nekorg/pawbar/pkg/menus/wire"
	"github.com/nekorg/pawbar/pkg/module"
)

const (
	// callTimeout bounds a round-trip we need the answer to. A tray app
	// that stops answering must not take the click path with it.
	callTimeout = 2 * time.Second
	// eventTimeout bounds one we do not: nothing waits on an event.
	eventTimeout = 1 * time.Second
	// refreshDebounce coalesces a burst of change signals. An applet
	// rebuilding its menu emits one per item it touched, and each on its
	// own would cost a GetLayout round-trip.
	refreshDebounce = 50 * time.Millisecond
)

// sessionBus is one connection for every tray menu this process opens. A
// fresh one per open put an auth handshake on the click path.
var sessionBus = sync.OnceValues(func() (*dbus.Conn, error) {
	return dbus.ConnectSessionBus()
})

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
	// owner is the unique name behind busname, for telling this item's
	// signals from those of another app exporting the same menu path.
	owner string

	rule string
	sigs chan *dbus.Signal
	done chan struct{}
	shut sync.Once

	mu  sync.Mutex
	rev uint32
}

func NewDBusMenuClient(busname, path string) (*DBusMenuClient, error) {
	conn, err := sessionBus()
	if err != nil {
		return nil, fmt.Errorf("error connecting to session bus: %w", err)
	}

	c := &DBusMenuClient{
		conn:    conn,
		obj:     conn.Object(busname, dbus.ObjectPath(path)),
		busname: busname,
		path:    path,
		owner:   busname,
		done:    make(chan struct{}),
	}
	if len(busname) > 0 && busname[0] != ':' {
		var owner string
		if call := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, busname); call.Err == nil {
			if call.Store(&owner) == nil {
				c.owner = owner
			}
		}
	}
	return c, nil
}

// Close drops this menu's subscription. The connection is shared with every
// other menu, so it stays up.
func (c *DBusMenuClient) Close() {
	c.shut.Do(func() {
		close(c.done)
		if c.sigs == nil {
			return
		}
		c.conn.RemoveSignal(c.sigs)
		c.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, c.rule)
	})
}

func (c *DBusMenuClient) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// call makes a bounded method call. Every dbusmenu call is on a path the
// user is waiting on, and none of them may block forever on a wedged app.
func (c *DBusMenuClient) call(timeout time.Duration, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.obj.CallWithContext(ctx, method, 0, args...)
}

func (c *DBusMenuClient) GetLayout() (Layout, error) {
	return c.GetLayoutForParent(0)
}

// GetLayoutForParent loads one level. Depth is 1 because convert renders
// only the children it is handed; asking for the whole subtree serialised a
// tree that was then thrown away, once per level.
func (c *DBusMenuClient) GetLayoutForParent(parentId int32) (Layout, error) {
	call := c.call(callTimeout, "com.canonical.dbusmenu.GetLayout", parentId, int32(1), []string{})
	if call.Err != nil {
		return Layout{}, fmt.Errorf("error calling GetLayout: %w", call.Err)
	}

	var revision uint32
	var layout Layout
	err := call.Store(&revision, &layout)
	if err != nil {
		return Layout{}, fmt.Errorf("error storing layout: %w", err)
	}
	c.seen(revision)

	return layout, nil
}

func (c *DBusMenuClient) SendEvent(id int32, eventType string, data interface{}) error {
	timestamp := uint32(time.Now().Unix())
	call := c.call(eventTimeout, "com.canonical.dbusmenu.Event", id, eventType, dbus.MakeVariant(data), timestamp)
	return call.Err
}

// notify sends an event without waiting for it. Clicks and hovers are
// dispatched from the menu's message pump, so a slow applet answering here
// would stall every message queued behind it.
func (c *DBusMenuClient) notify(id int32, eventType string) {
	go func() {
		if err := c.SendEvent(id, eventType, ""); err != nil {
			logging.Log.Warn().Msgf("dbusmenu: sending %s event: %v", eventType, err)
		}
	}()
}

func (c *DBusMenuClient) AboutToShow(id int32) (bool, error) {
	call := c.call(callTimeout, "com.canonical.dbusmenu.AboutToShow", id)
	if call.Err != nil {
		return false, call.Err
	}

	var needUpdate bool
	err := call.Store(&needUpdate)
	return needUpdate, err
}

// seen records the newest layout revision this client has fetched.
func (c *DBusMenuClient) seen(rev uint32) {
	c.mu.Lock()
	if rev > c.rev {
		c.rev = rev
	}
	c.mu.Unlock()
}

// stale reports a LayoutUpdated announcing a revision we have already
// fetched past. Chatty applets re-announce, and every announcement used to
// cost a full refetch.
func (c *DBusMenuClient) stale(rev uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return rev != 0 && rev < c.rev
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
//
// The order is the whole point. Subscribing first means the LayoutUpdated
// an applet emits while rebuilding is not announced into a void, and
// AboutToShow before the fetch is what makes applets that populate their
// menu lazily (nm-applet and everything else on libdbusmenu-gtk) populate
// it at all.
func launch(busname, path string, open func(*menus.List) (*menus.ListHandle, error)) (*menus.ListHandle, error) {
	client, err := NewDBusMenuClient(busname, path)
	if err != nil {
		return nil, err
	}

	client.subscribe()
	if _, err := client.AboutToShow(0); err != nil {
		logging.Log.Debug().Msgf("dbusmenu: AboutToShow on %s: %v", busname, err)
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

	client.notify(0, "opened")
	go client.watchSignals(h)
	go func() {
		<-h.Done()
		client.notify(0, "closed")
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

		// Absent means shown; only an explicit false hides. Ignoring this
		// rendered an applet's hidden rows as ordinary clickable ones.
		if visible, ok := prop[bool](l.Properties, "visible"); ok && !visible {
			continue
		}
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
				c.notify(id, "opened")
				return sub
			}
			it.OnSubmenuClose = func() { c.notify(id, "closed") }
		}

		it.OnClick = func() { c.notify(id, "clicked") }
		it.OnHover = func() { c.notify(id, "hovered") }

		items = append(items, it)
	}
	return items
}

// subscribe starts listening for changes. It runs before the first fetch:
// installed after one, it missed the LayoutUpdated an applet emits when
// AboutToShow makes it rebuild, and the menu then stayed stale for as long
// as it was open.
func (c *DBusMenuClient) subscribe() {
	c.rule = fmt.Sprintf("type='signal',sender='%s',path='%s',interface='com.canonical.dbusmenu'", c.busname, c.path)
	if call := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, c.rule); call.Err != nil {
		logging.Log.Warn().Msgf("dbusmenu: watching %s: %v", c.busname, call.Err)
		return
	}
	c.sigs = make(chan *dbus.Signal, 16)
	c.conn.Signal(c.sigs)
}

// watchSignals mirrors live dbusmenu changes into the open menu. It exits
// when the menu closes.
func (c *DBusMenuClient) watchSignals(h *menus.ListHandle) {
	if c.sigs == nil {
		return
	}
	var (
		mu    sync.Mutex
		timer *time.Timer
	)
	schedule := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer == nil {
			timer = time.AfterFunc(refreshDebounce, func() { c.refresh(h) })
			return
		}
		timer.Reset(refreshDebounce)
	}
	defer func() {
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
	}()

	for {
		select {
		case <-c.done:
			return
		case sig := <-c.sigs:
			if sig == nil || !c.mine(sig) {
				continue
			}
			switch sig.Name {
			case "com.canonical.dbusmenu.LayoutUpdated":
				if len(sig.Body) > 0 {
					if rev, ok := sig.Body[0].(uint32); ok && c.stale(rev) {
						continue
					}
				}
				schedule()
			case "com.canonical.dbusmenu.ItemsPropertiesUpdated":
				schedule()
			}
		}
	}
}

// mine tells this item's signals from another app's. The connection is
// shared across every open menu, and /MenuBar is a path plenty of apps
// export, so the match rule on its own is not enough.
func (c *DBusMenuClient) mine(sig *dbus.Signal) bool {
	return string(sig.Path) == c.path && (sig.Sender == c.owner || sig.Sender == c.busname)
}

// refresh reloads the menu from the top. Update walks the open submenu
// chain from there, so a submenu the user is looking at is reloaded too
// instead of showing what its source held when it opened.
func (c *DBusMenuClient) refresh(h *menus.ListHandle) {
	if c.closed() {
		return
	}
	items, err := c.itemsFor(0)
	if err != nil {
		logging.Log.Warn().Msgf("dbusmenu: refreshing layout after signal: %v", err)
		return
	}
	if c.closed() {
		return
	}
	h.Update(items)
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
