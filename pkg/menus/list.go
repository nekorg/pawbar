// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"strings"
	"sync"

	"github.com/codelif/xdgicons"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
	"github.com/nekorg/pawbar/pkg/module"
)

// Toggle marks for list items.
const (
	ToggleNone  = wire.ToggleNone
	ToggleCheck = wire.ToggleCheck
	ToggleRadio = wire.ToggleRadio
)

// Item is one entry of a declarative list menu. Callbacks run in the
// bar process; only the renderable data crosses to the panel.
type Item struct {
	Label     string
	Disabled  bool
	Separator bool // renders a rule; all other fields ignored

	Toggle  wire.Toggle
	Checked bool

	// Gutter content (one per item, priority: Toggle, Glyph, icon).
	// Every list menu has a fixed-width left gutter; labels always
	// start right after it, so rows stay aligned.
	Glyph    string // text glyph (e.g. a nerd-font icon)
	IconName string // xdg icon, resolved at open time
	IconData []byte // raw png, wins over IconName

	// Submenu is a static submenu; LoadSubmenu builds one on demand
	// (checked only when Submenu is nil). HasSubmenu forces the
	// submenu arrow for LoadSubmenu items.
	Submenu     []Item
	HasSubmenu  bool
	LoadSubmenu func() []Item

	// OnClick runs when the item is activated; the menu closes after
	// unless KeepOpen is set (then it redraws, e.g. for toggles).
	OnClick  func()
	KeepOpen bool
	OnHover  func()
}

// List is a declarative menu.
type List struct {
	Items []Item
	// Key subdivides the opening module for toggle purposes.
	Key string
	// OnClose runs once when the menu has closed (any reason).
	OnClose func()
}

// OpenList opens (or toggles) l near the anchor. Fire-and-forget; the
// menu's lifetime runs on a runtime-tracked goroutine.
func OpenList(ctx *module.Ctx, at Anchor, l *List) error {
	ctx.Go(func() {
		h, err := openList(owner{id: ctx, key: l.Key}, at, l)
		if err != nil {
			logging.Log.Error().Msgf("menus: opening list: %v", err)
			return
		}
		if h != nil {
			<-h.Done()
		}
	})
	return nil
}

// OpenListH opens l and returns a handle for live updates. It spawns
// synchronously: call it off the module goroutine (e.g. inside
// ctx.Go). A nil handle with nil error means the click toggled the
// menu closed.
func OpenListH(ctx *module.Ctx, at Anchor, l *List) (*ListHandle, error) {
	return openList(owner{id: ctx, key: l.Key}, at, l)
}

// LaunchList opens l without a module owner; for standalone binaries.
// Wait on the returned handle's Done to block until it closes.
func LaunchList(at Anchor, l *List) (*ListHandle, error) {
	return openList(owner{id: l, key: l.Key}, at, l)
}

// ListHandle drives a live list menu.
type ListHandle struct {
	ctrl *listCtrl
	root *Handle
}

// Update replaces the root level's items; the panel resizes (and
// re-clamps) itself to fit.
func (lh *ListHandle) Update(items []Item) {
	wi := lh.ctrl.wireLevel(items)
	if err := lh.root.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi}); err != nil {
		logging.Log.Warn().Msgf("menus: sending list update: %v", err)
	}
}

// Close closes the menu and any submenus.
func (lh *ListHandle) Close() { lh.root.Close() }

// Done closes when the menu has fully closed.
func (lh *ListHandle) Done() <-chan struct{} { return lh.root.Done() }

func openList(o owner, at Anchor, l *List) (*ListHandle, error) {
	c := &listCtrl{reg: make(map[int32]listEntry)}
	wi := c.wireLevel(l.Items)
	w, h := listDims(wi)
	root, err := openRoot(o, "pawmenu", at, w, h, true)
	if err != nil || root == nil {
		return nil, err
	}
	geo := root.Geometry()
	if err := root.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi, Geo: &geo}); err != nil {
		logging.Log.Warn().Msgf("menus: sending initial items: %v", err)
	}
	go c.run(root)
	if l.OnClose != nil {
		onClose := l.OnClose
		go func() {
			<-root.Done()
			onClose()
		}()
	}
	return &ListHandle{ctrl: c, root: root}, nil
}

// listEntry locates an Item inside its level slice, so toggle
// bookkeeping can flip siblings.
type listEntry struct {
	level []Item
	idx   int
}

type listCtrl struct {
	mu     sync.Mutex
	nextID int32
	reg    map[int32]listEntry
}

// iconLookup builds the xdg icon index lazily: only the bar resolves icon
// paths (menu children receive already-resolved paths), so warm menu
// spares never pay the icon-theme scan at process start.
var iconLookup = sync.OnceValue(func() *xdgicons.IconLookup {
	return xdgicons.NewIconLookupWithConfig(xdgicons.LookupConfig{FallbackTheme: "Adwaita"})
})

func resolveIconPath(name string) string {
	if name == "" {
		return ""
	}
	var icon xdgicons.Icon
	if strings.HasSuffix(name, "-symbolic") {
		icon, _ = iconLookup().Lookup(name)
	} else {
		icon, _ = iconLookup().FindBestIcon([]string{name + "-symbolic", name}, 48, 2)
	}
	return icon.Path
}

// wireLevel registers one level of items and projects it to the wire.
func (c *listCtrl) wireLevel(level []Item) []wire.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]wire.Item, 0, len(level))
	for i := range level {
		it := &level[i]
		c.nextID++
		id := c.nextID
		c.reg[id] = listEntry{level: level, idx: i}
		out = append(out, wire.Item{
			ID:         id,
			Label:      it.Label,
			Disabled:   it.Disabled,
			Separator:  it.Separator,
			Toggle:     it.Toggle,
			Checked:    it.Checked,
			HasSubmenu: it.HasSubmenu || len(it.Submenu) > 0,
			Glyph:      it.Glyph,
			IconName:   it.IconName,
			IconPath:   resolveIconPath(it.IconName),
			IconData:   it.IconData,
		})
	}
	return out
}

func (c *listCtrl) lookup(id int32) (listEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.reg[id]
	return e, ok
}

// applyToggle flips check/radio state on the clicked item (and its
// radio siblings) so a KeepOpen redraw shows the new state.
func (c *listCtrl) applyToggle(e listEntry) {
	it := &e.level[e.idx]
	switch it.Toggle {
	case wire.ToggleCheck:
		it.Checked = !it.Checked
	case wire.ToggleRadio:
		for i := range e.level {
			if e.level[i].Toggle == wire.ToggleRadio {
				e.level[i].Checked = i == e.idx
			}
		}
	}
}

// run consumes one panel's messages and drives callbacks, submenus and
// close semantics. Each spawned submenu panel gets its own run.
func (c *listCtrl) run(h *Handle) {
	for m := range h.Messages() {
		switch m.Type {
		case wire.MsgClicked:
			if m.ItemID < 0 {
				// Click outside any menu surface.
				h.Close()
				continue
			}
			e, ok := c.lookup(m.ItemID)
			if !ok {
				continue
			}
			it := &e.level[e.idx]
			if it.Disabled || it.Separator {
				continue
			}
			c.applyToggle(e)
			if it.OnClick != nil {
				it.OnClick()
			}
			if it.KeepOpen {
				wi := c.wireLevel(e.level)
				h.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi})
			} else {
				h.Close()
			}

		case wire.MsgHovered:
			if e, ok := c.lookup(m.ItemID); ok {
				if fn := e.level[e.idx].OnHover; fn != nil {
					fn()
				}
			}

		case wire.MsgSubmenuReq:
			e, ok := c.lookup(m.ItemID)
			if !ok {
				continue
			}
			it := &e.level[e.idx]
			sub := it.Submenu
			if sub == nil && it.LoadSubmenu != nil {
				sub = it.LoadSubmenu()
			}
			if len(sub) == 0 {
				continue
			}
			wi := c.wireLevel(sub)
			w, ht := listDims(wi)
			sh, err := h.OpenSub("pawmenu", m.Row, w, ht)
			if err != nil {
				logging.Log.Warn().Msgf("menus: opening submenu: %v", err)
				continue
			}
			geo := sh.Geometry()
			sh.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi, Geo: &geo})
			go c.run(sh)

		case wire.MsgSubmenuCancel:
			h.CloseBelow()
		}
	}
}

// listDims computes a list menu's size in cells: the widest label plus
// the canonical gutter and right pad. Gutter content (toggle marks,
// glyphs, icons) renders inside the fixed gutter, so it never widens
// the menu. Kept in one place because parent (initial spawn) and child
// (resize on update) must agree.
func listDims(items []wire.Item) (int, int) {
	maxLen := 0
	for _, it := range items {
		if n := len(it.Label); n > maxLen {
			maxLen = n
		}
	}
	w := maxLen + gutterCells + rightPadCells
	h := len(items)
	if h < 1 {
		h = 1
	}
	return w, h
}
