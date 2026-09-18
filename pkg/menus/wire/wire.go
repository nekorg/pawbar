// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package wire is the CBOR protocol spoken between the bar process and a
// menu panel over the katnip shmstream. Only data crosses the boundary;
// callbacks stay in the bar process.
package wire

type MsgType int

const (
	// parent -> child
	MsgUpdate MsgType = iota // replace the item list (and optionally geometry)
	MsgClose                 // exit now
	MsgOpen                  // become Kind, size to Cols x Rows, reveal at Geo
	MsgPlace                 // move to Geo; sent when what this panel hangs off moved or resized

	// child -> parent
	MsgClicked       // item activated (click or Enter)
	MsgHovered       // pointer or key selection moved onto item
	MsgSubmenuReq    // open the submenu of item; Row is the item's row in cells
	MsgSubmenuCancel // close the submenu of item
	MsgFocusLost     // panel lost keyboard focus (sent only after first focus)
	MsgFocusGained   // panel gained keyboard focus
	MsgResized       // panel resized itself; Geo carries the new position/size
	MsgReady         // host is warm (mapped off-screen) and awaiting MsgOpen
	MsgClosed        // menu is done and the host has parked itself warm again
)

type Toggle int8

const (
	ToggleNone Toggle = iota
	ToggleCheck
	ToggleRadio
)

// Item is the renderable projection of a menu item. Toggle, Glyph and
// the icon are alternative gutter contents, in that priority order.
type Item struct {
	ID         int32
	Label      string
	Disabled   bool
	Separator  bool
	Toggle     Toggle
	Checked    bool
	HasSubmenu bool
	Glyph      string // text glyph shown in the gutter
	IconName   string // for symbolic-detection in the renderer
	IconPath   string // resolved icon file path (parent does the xdg lookup)
	IconData   []byte // raw png, wins over IconPath
}

// Geometry gives a panel everything it needs to place itself after a live
// resize. All lengths are compositor-logical units except the
// pixels-per-cell pair, which is physical.
//
// The placement fields record what the panel was positioned *against*, not
// just where it ended up: a panel that only knows its own position can
// clamp itself back on-screen but cannot re-place itself, so it drifts as
// soon as its size changes.
type Geometry struct {
	MonW, MonH     int     // monitor size
	PanelX, PanelY int     // panel position as placed
	PPCX, PPCY     float64 // physical pixels per cell
	Scale          float64 // physical px / logical unit
	Pad            int     // extra logical units per edge (panel chrome)

	AnchorX, AnchorY int // root: the opening click, before clamping
	ParentX, ParentY int // submenu: where its parent panel sits
	ParentW          int // submenu: parent width in cells; 0 means this is a root
	Row              int // submenu: the parent row it hangs from
}

// IsSub reports whether g places a submenu against a parent panel rather
// than a root menu against a click.
func (g Geometry) IsSub() bool { return g.ParentW > 0 }

type Msg struct {
	Type       MsgType
	Kind       string    `cbor:",omitempty"` // menu renderer to run (MsgOpen)
	Items      []Item    `cbor:",omitempty"`
	ItemID     int32     `cbor:",omitempty"`
	Row        int       `cbor:",omitempty"`
	Cols, Rows int       `cbor:",omitempty"` // panel size in cells (MsgOpen, MsgResized)
	Geo        *Geometry `cbor:",omitempty"`
}
