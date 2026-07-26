// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sni

import (
	"image"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestApplyChangesReplacesAndClearsIconPixmap(t *testing.T) {
	events := make(chan Event, 2)
	s := &Service{listeners: []chan<- Event{events}}
	it := &Item{
		BusName:    ":1.42",
		Path:       "/StatusNotifierItem",
		IconName:   "old-icon",
		IconPixmap: image.NewNRGBA(image.Rect(0, 0, 1, 1)),
	}

	s.applyChanges(it, map[string]dbus.Variant{
		"IconName": dbus.MakeVariant("new-icon"),
		"IconPixmap": dbus.MakeVariant([][]any{{
			int32(1), int32(1), []byte{0xff, 0x11, 0x22, 0x33},
		}}),
	})

	if it.IconName != "new-icon" {
		t.Fatalf("IconName = %q, want new-icon", it.IconName)
	}
	img, ok := it.IconPixmap.(*image.NRGBA)
	if !ok {
		t.Fatalf("IconPixmap type = %T, want *image.NRGBA", it.IconPixmap)
	}
	got := img.NRGBAAt(0, 0)
	if got.R != 0x11 || got.G != 0x22 || got.B != 0x33 || got.A != 0xff {
		t.Fatalf("decoded pixel = %#v, want R=11 G=22 B=33 A=ff", got)
	}
	ev := <-events
	if ev.Kind != ItemChanged || ev.ID != ":1.42/StatusNotifierItem" || ev.Item.IconName != "new-icon" {
		t.Fatalf("event = %#v, want changed item snapshot", ev)
	}

	s.applyChanges(it, map[string]dbus.Variant{
		"IconPixmap": dbus.MakeVariant([][]any{}),
	})

	if it.IconPixmap != nil {
		t.Fatalf("IconPixmap = %T, want nil after empty update", it.IconPixmap)
	}
	ev = <-events
	if ev.Item.IconPixmap != nil {
		t.Fatalf("event IconPixmap = %T, want nil", ev.Item.IconPixmap)
	}
}
