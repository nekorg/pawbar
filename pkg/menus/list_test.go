// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

func TestWireLevelAssignsUniqueResolvableIDs(t *testing.T) {
	c := &listCtrl{reg: make(map[int32]listEntry)}
	level := []Item{
		{Label: "one"},
		{Label: "two", Submenu: []Item{{Label: "child"}}},
		{Separator: true},
	}
	wi := c.wireLevel(level)
	if len(wi) != 3 {
		t.Fatalf("wire items = %d, want 3", len(wi))
	}
	seen := map[int32]bool{}
	for i, w := range wi {
		if seen[w.ID] {
			t.Errorf("duplicate id %d", w.ID)
		}
		seen[w.ID] = true
		e, ok := c.lookup(w.ID)
		if !ok {
			t.Fatalf("id %d not in registry", w.ID)
		}
		if e.idx != i {
			t.Errorf("id %d resolves to index %d, want %d", w.ID, e.idx, i)
		}
	}
	if !wi[1].HasSubmenu {
		t.Error("item with static submenu should have HasSubmenu")
	}
	if !wi[2].Separator {
		t.Error("separator lost in projection")
	}
}

func TestApplyToggleRadioFlipsSiblings(t *testing.T) {
	c := &listCtrl{reg: make(map[int32]listEntry)}
	level := []Item{
		{Label: "a", Toggle: ToggleRadio, Checked: true},
		{Label: "b", Toggle: ToggleRadio},
		{Label: "plain"},
		{Label: "c", Toggle: ToggleRadio},
	}
	wi := c.wireLevel(level)
	e, _ := c.lookup(wi[3].ID)
	c.applyToggle(e)
	want := []bool{false, false, false, true}
	for i, w := range want {
		if level[i].Checked != w {
			t.Errorf("item %d Checked = %v, want %v", i, level[i].Checked, w)
		}
	}
}

func TestApplyToggleCheckFlips(t *testing.T) {
	c := &listCtrl{reg: make(map[int32]listEntry)}
	level := []Item{{Label: "a", Toggle: ToggleCheck}}
	wi := c.wireLevel(level)
	e, _ := c.lookup(wi[0].ID)
	c.applyToggle(e)
	if !level[0].Checked {
		t.Error("check item should be checked after toggle")
	}
	c.applyToggle(e)
	if level[0].Checked {
		t.Error("check item should be unchecked after second toggle")
	}
}

func TestListDims(t *testing.T) {
	items := []wire.Item{
		{Label: "short"},
		{Label: "the longest label"}, // 17 bytes
		{Separator: true},
	}
	w, h := listDims(items)
	if want := 17 + gutterCells + rightPadCells; w != want {
		t.Errorf("width = %d, want %d", w, want)
	}
	if h != 3 {
		t.Errorf("height = %d, want 3", h)
	}

	// Gutter content (icons, toggles, glyphs) renders inside the
	// fixed gutter and never widens the menu.
	items[0].IconName = "network-wireless"
	items[1].Toggle = wire.ToggleRadio
	items[0].Glyph = "x"
	if w2, _ := listDims(items); w2 != w {
		t.Errorf("width with gutter content = %d, want %d", w2, w)
	}

	// Never zero-sized.
	if _, h := listDims(nil); h != 1 {
		t.Errorf("empty list height = %d, want 1", h)
	}
}

func TestWireMsgRoundTrip(t *testing.T) {
	geo := wire.Geometry{MonW: 1000, MonH: 500, PanelX: 12, PanelY: 34, PPCX: 10.5, PPCY: 21.25, Scale: 2, Pad: 2}
	in := wire.Msg{
		Type: wire.MsgUpdate,
		Items: []wire.Item{
			{ID: 7, Label: "hello", Toggle: wire.ToggleCheck, Checked: true, IconData: []byte{1, 2, 3}},
			{ID: 8, Separator: true},
		},
		Geo: &geo,
	}
	var buf bytes.Buffer
	if err := cbor.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out wire.Msg
	if err := cbor.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Type != in.Type || len(out.Items) != 2 || out.Geo == nil {
		t.Fatalf("round trip mangled message: %+v", out)
	}
	if !reflect.DeepEqual(out.Items, in.Items) {
		t.Errorf("items mangled: %+v", out.Items)
	}
	if *out.Geo != geo {
		t.Errorf("geometry mangled: %+v", *out.Geo)
	}
}
