// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package dbusmenukitty

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

func props(kv map[string]any) map[string]dbus.Variant {
	out := make(map[string]dbus.Variant, len(kv))
	for k, v := range kv {
		out[k] = dbus.MakeVariant(v)
	}
	return out
}

func TestConvertHonoursVisible(t *testing.T) {
	c := &DBusMenuClient{}
	layout := Layout{Children: []Layout{
		{Id: 1, Properties: props(map[string]any{"label": "shown"})},
		{Id: 2, Properties: props(map[string]any{"label": "hidden", "visible": false})},
		{Id: 3, Properties: props(map[string]any{"label": "explicit", "visible": true})},
	}}

	got := c.convert(layout)
	if len(got) != 2 {
		t.Fatalf("converted %d items, want 2", len(got))
	}
	if got[0].Label != "shown" || got[1].Label != "explicit" {
		t.Errorf("wrong items survived: %q, %q", got[0].Label, got[1].Label)
	}
}

func TestConvertMapsProperties(t *testing.T) {
	c := &DBusMenuClient{}
	layout := Layout{Children: []Layout{
		{Id: 1, Properties: props(map[string]any{"type": "separator"})},
		{Id: 2, Properties: props(map[string]any{"label": "_Quit", "enabled": false})},
		{Id: 3, Properties: props(map[string]any{
			"label": "Mute", "toggle-type": "checkmark", "toggle-state": int32(1),
		})},
		{Id: 4, Properties: props(map[string]any{"label": "More", "children-display": "submenu"})},
	}}

	got := c.convert(layout)
	if len(got) != 4 {
		t.Fatalf("converted %d items, want 4", len(got))
	}
	if !got[0].Separator {
		t.Error("separator lost")
	}
	if got[1].Label != "Quit" || !got[1].Disabled {
		t.Errorf("item 1 = %q disabled=%v, want \"Quit\" disabled=true", got[1].Label, got[1].Disabled)
	}
	if got[2].Toggle != wire.ToggleCheck || !got[2].Checked {
		t.Errorf("toggle = %v checked=%v, want check/true", got[2].Toggle, got[2].Checked)
	}
	if !got[3].HasSubmenu || got[3].LoadSubmenu == nil || got[3].OnSubmenuClose == nil {
		t.Error("submenu item is missing its submenu wiring")
	}
}

// An off-spec property type is common enough in the wild that it must not
// take the whole item with it.
func TestConvertSurvivesWrongPropertyTypes(t *testing.T) {
	c := &DBusMenuClient{}
	layout := Layout{Children: []Layout{
		{Id: 1, Properties: props(map[string]any{"label": "ok", "enabled": "yes", "visible": int32(1)})},
	}}

	got := c.convert(layout)
	if len(got) != 1 || got[0].Label != "ok" {
		t.Fatalf("got %+v, want the item to survive", got)
	}
	if got[0].Disabled {
		t.Error("an unreadable enabled property should not disable the item")
	}
}

func TestStaleSkipsRevisionsAlreadyFetched(t *testing.T) {
	c := &DBusMenuClient{}
	c.seen(5)

	if !c.stale(4) {
		t.Error("revision 4 is behind 5 and should be skipped")
	}
	if c.stale(5) {
		t.Error("revision 5 should still be taken: an applet may reuse it")
	}
	if c.stale(6) {
		t.Error("revision 6 is newer and must never be skipped")
	}
	// Implementations that do not carry a revision must not be filtered out.
	if c.stale(0) {
		t.Error("a missing revision should never count as stale")
	}

	// An out-of-order fetch must not walk the high-water mark backwards.
	c.seen(3)
	if !c.stale(4) {
		t.Error("seen walked the revision back to 3")
	}
}

func TestParseLabel(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		key      rune
		at       int
		found    bool
	}{
		{in: "File", want: "File"},
		{in: "_File", want: "File", key: 'F', at: 0, found: true},
		{in: "Save _As", want: "Save As", key: 'A', at: 5, found: true},
		{in: "__", want: "_"},
		{in: "a__b_c", want: "a_bc", key: 'c', at: 3, found: true},
	} {
		got := ParseLabel(tc.in)
		if got.Display != tc.want {
			t.Errorf("ParseLabel(%q).Display = %q, want %q", tc.in, got.Display, tc.want)
		}
		if got.Found != tc.found || (tc.found && (got.AccessKey != tc.key || got.AccessIndex != tc.at)) {
			t.Errorf("ParseLabel(%q) key = %q@%d found=%v, want %q@%d found=%v",
				tc.in, got.AccessKey, got.AccessIndex, got.Found, tc.key, tc.at, tc.found)
		}
	}
}
