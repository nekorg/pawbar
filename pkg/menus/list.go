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
	"go.rockorager.dev/vaxis"
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
	// submenu arrow for LoadSubmenu items. OnSubmenuClose runs when the
	// submenu panel goes away, for sources that are told when a menu is
	// no longer on screen.
	Submenu        []Item
	HasSubmenu     bool
	LoadSubmenu    func() []Item
	OnSubmenuClose func()

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

// Update replaces the root level's items and reloads any submenu still open
// below them; each panel resizes (and re-clamps) itself to fit.
func (lh *ListHandle) Update(items []Item) {
	lh.ctrl.refresh(lh.root, items)
}

// Close closes the menu and any submenus.
func (lh *ListHandle) Close() { lh.root.Close() }

// Done closes when the menu has fully closed.
func (lh *ListHandle) Done() <-chan struct{} { return lh.root.Done() }

func openList(o owner, at Anchor, l *List) (*ListHandle, error) {
	c := newListCtrl()
	wi := c.wireLevel(rootLevel, l.Items)
	w, h := listDims(wi)
	root, err := openRoot(o, "pawmenu", at, w, h, true)
	if err != nil || root == nil {
		return nil, err
	}
	geo := root.Geometry()
	if err := root.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi, Geo: &geo}); err != nil {
		logging.Log.Warn().Msgf("menus: sending initial items: %v", err)
	}
	go c.run(root, rootLevel)
	if l.OnClose != nil {
		onClose := l.OnClose
		go func() {
			<-root.Done()
			onClose()
		}()
	}
	return &ListHandle{ctrl: c, root: root}, nil
}

// rootLevel is the level id of a menu's top panel; submenus get their own
// as they open.
const rootLevel int32 = 0

// listEntry locates an Item inside its level, so toggle bookkeeping can
// flip siblings and a refresh can find the item a submenu hangs off.
type listEntry struct {
	level int32
	idx   int
}

// levelState is one panel's worth of the menu: the items it holds, the wire
// ids currently standing for them, and the submenu open from it. At most one
// submenu is open per level, since opening another closes the first.
type levelState struct {
	items []Item
	ids   []int32
	sub   *openSub
}

// openSub is a live submenu panel and the item it hangs off, by index rather
// than by wire id: ids are reissued on every refresh, the row is not.
type openSub struct {
	parent int
	level  int32
	h      *Handle
}

type listCtrl struct {
	mu        sync.Mutex
	nextID    int32
	nextLevel int32
	reg       map[int32]listEntry
	levels    map[int32]*levelState
}

func newListCtrl() *listCtrl {
	return &listCtrl{
		reg:    make(map[int32]listEntry),
		levels: make(map[int32]*levelState),
	}
}

// iconLookup builds the xdg icon index lazily: only the bar resolves icon
// paths (menu children receive already-resolved paths), so warm menu
// spares never pay the icon-theme scan at process start.
var iconLookup = sync.OnceValue(func() *xdgicons.IconLookup {
	return xdgicons.NewIconLookupWithConfig(xdgicons.LookupConfig{FallbackTheme: "Adwaita"})
})

// themeIconSize is the nominal size to ask the theme for. A menu icon is
// drawn one row tall, so this is the size band a theme draws its small icons
// at; asking for something much larger gets artwork drawn with more padding,
// which lands smaller in the same box.
const themeIconSize = 24

func resolveIconPath(name string) string {
	if name == "" {
		return ""
	}
	var icon xdgicons.Icon
	if strings.HasSuffix(name, "-symbolic") {
		icon, _ = iconLookup().FindIcon(name, themeIconSize, 1)
	} else {
		icon, _ = iconLookup().FindBestIcon([]string{name + "-symbolic", name}, themeIconSize, 1)
	}
	return icon.Path
}

// wireLevel installs items as level lvl and projects them to the wire.
func (c *listCtrl) wireLevel(lvl int32, items []Item) []wire.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.register(lvl, items)
}

// register replaces what a level holds, retiring the ids it had first. A
// menu refreshed on every source change would otherwise grow the registry
// for its whole lifetime, and stale ids would go on resolving to items the
// user can no longer see.
//
// Caller holds c.mu.
func (c *listCtrl) register(lvl int32, items []Item) []wire.Item {
	st := c.levels[lvl]
	if st == nil {
		st = &levelState{}
		c.levels[lvl] = st
	}
	for _, id := range st.ids {
		delete(c.reg, id)
	}
	st.items = items
	st.ids = make([]int32, 0, len(items))

	out := make([]wire.Item, 0, len(items))
	for i := range items {
		it := &items[i]
		c.nextID++
		id := c.nextID
		c.reg[id] = listEntry{level: lvl, idx: i}
		st.ids = append(st.ids, id)
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

// item resolves a wire id to the item it stands for. The pointer is into the
// level's slice, so a refresh that lands first leaves it pointing at what the
// user was looking at when they clicked, which is the right answer anyway.
func (c *listCtrl) item(id int32) (*Item, listEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.reg[id]
	if !ok {
		return nil, e, false
	}
	return c.at(e.level, e.idx)
}

// at resolves a position. Caller holds c.mu.
func (c *listCtrl) at(lvl int32, idx int) (*Item, listEntry, bool) {
	st := c.levels[lvl]
	if st == nil || idx < 0 || idx >= len(st.items) {
		return nil, listEntry{}, false
	}
	return &st.items[idx], listEntry{level: lvl, idx: idx}, true
}

// applyToggle flips check/radio state on the clicked item (and its
// radio siblings) so a KeepOpen redraw shows the new state.
func (c *listCtrl) applyToggle(e listEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.levels[e.level]
	if st == nil || e.idx < 0 || e.idx >= len(st.items) {
		return
	}
	it := &st.items[e.idx]
	switch it.Toggle {
	case wire.ToggleCheck:
		it.Checked = !it.Checked
	case wire.ToggleRadio:
		for i := range st.items {
			if st.items[i].Toggle == wire.ToggleRadio {
				st.items[i].Checked = i == e.idx
			}
		}
	}
}

// resend re-projects a level as it now stands, for a redraw after a toggle.
func (c *listCtrl) resend(lvl int32) []wire.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.levels[lvl]
	if st == nil {
		return nil
	}
	return c.register(lvl, st.items)
}

// newLevel reserves a level id for a submenu about to open.
func (c *listCtrl) newLevel() int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextLevel++
	return c.nextLevel
}

// attach records a submenu as open below its parent item.
func (c *listCtrl) attach(parent listEntry, lvl int32, h *Handle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.levels[parent.level]; st != nil {
		st.sub = &openSub{parent: parent.idx, level: lvl, h: h}
	}
}

// drop forgets a level once its panel is gone: its ids are retired and any
// parent stops pointing at it.
func (c *listCtrl) drop(lvl int32) {
	c.mu.Lock()
	if st := c.levels[lvl]; st != nil {
		for _, id := range st.ids {
			delete(c.reg, id)
		}
		delete(c.levels, lvl)
	}
	var closed []func()
	for key, st := range c.levels {
		if st.sub == nil || st.sub.level != lvl {
			continue
		}
		if it, _, ok := c.at(key, st.sub.parent); ok && it.OnSubmenuClose != nil {
			closed = append(closed, it.OnSubmenuClose)
		}
		st.sub = nil
	}
	c.mu.Unlock()

	// Off the lock: a source told about the close is free to call back in.
	for _, fn := range closed {
		fn()
	}
}

// sub reports the submenu open from a level, if any.
func (c *listCtrl) sub(lvl int32) *openSub {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.levels[lvl]; st != nil {
		return st.sub
	}
	return nil
}

// refresh replaces the root items and walks the open submenu chain,
// reloading each panel from the item it hangs off. Updating only the root
// would leave a submenu the user is looking at showing what its source held
// when it opened.
func (c *listCtrl) refresh(root *Handle, items []Item) {
	send(root, c.wireLevel(rootLevel, items))

	lvl, h := rootLevel, root
	for {
		sub := c.sub(lvl)
		if sub == nil {
			return
		}
		next, ok := c.reload(lvl, sub.parent)
		if !ok {
			// The item this submenu hung off is gone or has nothing to
			// show any more; the rest of the chain goes with it.
			h.CloseBelow()
			return
		}
		send(sub.h, c.wireLevel(sub.level, next))
		lvl, h = sub.level, sub.h
	}
}

// reload asks the item at a position for its submenu contents again.
func (c *listCtrl) reload(lvl int32, idx int) ([]Item, bool) {
	c.mu.Lock()
	it, _, ok := c.at(lvl, idx)
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	sub := it.Submenu
	if sub == nil && it.LoadSubmenu != nil {
		sub = it.LoadSubmenu()
	}
	return sub, len(sub) > 0
}

func send(h *Handle, wi []wire.Item) {
	if err := h.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi}); err != nil {
		logging.Log.Warn().Msgf("menus: sending list update: %v", err)
	}
}

// run consumes one panel's messages and drives callbacks, submenus and
// close semantics. Each spawned submenu panel gets its own run, on its own
// level.
func (c *listCtrl) run(h *Handle, lvl int32) {
	if lvl != rootLevel {
		defer c.drop(lvl)
	}
	for m := range h.Messages() {
		switch m.Type {
		case wire.MsgClicked:
			if m.ItemID < 0 {
				// Click outside any menu surface.
				h.Close()
				continue
			}
			it, e, ok := c.item(m.ItemID)
			if !ok {
				continue
			}
			if it.Disabled || it.Separator {
				continue
			}
			c.applyToggle(e)
			if it.OnClick != nil {
				it.OnClick()
			}
			if it.KeepOpen {
				send(h, c.resend(e.level))
			} else {
				h.Close()
			}

		case wire.MsgHovered:
			if it, _, ok := c.item(m.ItemID); ok && it.OnHover != nil {
				it.OnHover()
			}

		case wire.MsgSubmenuReq:
			it, e, ok := c.item(m.ItemID)
			if !ok {
				continue
			}
			sub := it.Submenu
			if sub == nil && it.LoadSubmenu != nil {
				sub = it.LoadSubmenu()
			}
			if len(sub) == 0 {
				continue
			}
			child := c.newLevel()
			wi := c.wireLevel(child, sub)
			w, ht := listDims(wi)
			sh, err := h.OpenSub("pawmenu", m.Row, w, ht)
			if err != nil {
				logging.Log.Warn().Msgf("menus: opening submenu: %v", err)
				c.drop(child)
				continue
			}
			c.attach(e, child, sh)
			geo := sh.Geometry()
			sh.Send(wire.Msg{Type: wire.MsgUpdate, Items: wi, Geo: &geo})
			go c.run(sh, child)

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
		if n := labelCells(it.Label); n > maxLen {
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

// labelCells is a label's width in terminal cells. Byte length is not it:
// a CJK grapheme takes two cells and three bytes, and every menu here is
// sized from this number.
func labelCells(s string) int {
	if isASCII(s) {
		return len(s)
	}
	n := 0
	it := vaxis.NewCharacterIterator(s)
	for c, ok := it.Next(); ok; c, ok = it.Next() {
		n += c.Width
	}
	return n
}

// isASCII reports whether s is plain ascii, where one byte is one cell.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
