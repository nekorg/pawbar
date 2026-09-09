// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sink

import (
	"strings"
	"testing"

	"github.com/nekorg/pawbar/internal/services/pulse"
)

func state() pulse.State {
	return pulse.State{Connected: true, Default: "hdmi1", Sinks: []pulse.SinkInfo{
		{Name: "hdmi3", Label: "HDMI 3", Volume: 100, Available: false},
		{Name: "hdmi2", Label: "HDMI 2", Volume: 100, Available: false},
		{Name: "hdmi1", Label: "LG FHD", Volume: 40, Available: true},
		{Name: "speaker", Label: "Speaker", Volume: 55, Available: true},
	}}
}

func got(t *testing.T, availableOnly bool) []string {
	t.Helper()
	l := Menu(state(), availableOnly, func(string) error { return nil })
	out := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		out = append(out, it.Label)
	}
	return out
}

func TestMenuHidesUnpluggedOutputs(t *testing.T) {
	items := got(t, true)
	if len(items) != 2 {
		t.Fatalf("got %d items %q, want the 2 usable ones", len(items), items)
	}
	for _, it := range items {
		if strings.HasPrefix(it, "HDMI") {
			t.Errorf("unplugged output %q is still listed", it)
		}
	}
}

func TestMenuShowsEverythingWhenDisabled(t *testing.T) {
	if items := got(t, false); len(items) != 4 {
		t.Errorf("got %d items %q, want all 4", len(items), items)
	}
}

// The volume column is right-aligned by padding names to a common width,
// so every row must be the same length and end in the percentage.
func TestMenuAlignsVolumes(t *testing.T) {
	items := got(t, false)
	for _, it := range items {
		if len(it) != len(items[0]) {
			t.Fatalf("row %q is not the same width as %q", it, items[0])
		}
		if !strings.HasSuffix(it, "%") {
			t.Errorf("row %q does not end in a percentage", it)
		}
	}
	if !strings.HasSuffix(items[2], "  40%") {
		t.Errorf("row %q: two-digit volume should be padded to the same column", items[2])
	}
}

// Hiding the sink you are listening through leaves a menu with no
// checked row.
func TestMenuKeepsTheDefaultEvenIfUnavailable(t *testing.T) {
	st := state()
	st.Sinks[2].Available = false
	l := Menu(st, true, func(string) error { return nil })

	found := false
	for _, it := range l.Items {
		if strings.HasPrefix(it.Label, "LG FHD") {
			found = true
			if !it.Checked {
				t.Error("the default sink is listed but not checked")
			}
		}
	}
	if !found {
		t.Error("the default sink was filtered out")
	}
}

func TestMenuFallsBackWhenFilterEmptiesTheList(t *testing.T) {
	st := state()
	st.Default = ""
	for i := range st.Sinks {
		st.Sinks[i].Available = false
	}
	if items := len(Menu(st, true, func(string) error { return nil }).Items); items != 4 {
		t.Errorf("got %d items, want all 4 rather than an empty menu", items)
	}
}

func TestMenuWithoutAServer(t *testing.T) {
	l := Menu(pulse.State{}, true, func(string) error { return nil })
	if len(l.Items) != 1 || !l.Items[0].Disabled {
		t.Errorf("want a single disabled row, got %+v", l.Items)
	}
}
