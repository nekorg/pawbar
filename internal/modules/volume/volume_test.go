// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package volume

import (
	"testing"

	"github.com/nekorg/pawbar/internal/menus/sink"
	"github.com/nekorg/pawbar/internal/services/pulse"
)

func st(defName string, vol float64) pulse.State {
	return pulse.State{Connected: true, Default: defName, Sinks: []pulse.SinkInfo{
		{Name: "a", Label: "LG FHD", Volume: vol, Available: true},
		{Name: "b", Label: "Speaker", Volume: 55, Available: true},
	}}
}

func sig(s pulse.State) string {
	return itemSig(sink.Items(s, true, func(string) error { return nil }))
}

// The open menu only redraws when something it shows actually moved.
func TestItemSigTracksWhatTheMenuShows(t *testing.T) {
	base := sig(st("a", 40))

	if sig(st("a", 40)) != base {
		t.Error("an identical snapshot should not redraw the menu")
	}
	if sig(st("a", 45)) == base {
		t.Error("a volume change should redraw the menu")
	}
	if sig(st("b", 40)) == base {
		t.Error("a default-sink change should redraw the menu")
	}

	renamed := st("a", 40)
	renamed.Sinks[0].Label = "WH-1000XM4"
	if sig(renamed) == base {
		t.Error("a renamed device should redraw the menu")
	}

	gone := st("a", 40)
	gone.Sinks[1].Available = false
	if sig(gone) == base {
		t.Error("a device becoming unavailable should redraw the menu")
	}

	// muting is not shown in the menu, so it must not churn the panel.
	muted := st("a", 40)
	muted.Sinks[0].Muted = true
	if sig(muted) != base {
		t.Error("a change the menu does not show should not redraw it")
	}
}
