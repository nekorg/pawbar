// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sink

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/nekorg/pawbar/internal/services/pulse"
	"github.com/nekorg/pawbar/pkg/menus"
)

// Menu builds the output-device picker from a state snapshot. set
// switches the default sink; the caller's own subscription is what
// brings the bar back in sync afterwards.
func Menu(st pulse.State, availableOnly bool, set func(name string) error) *menus.List {
	if !st.Connected {
		return &menus.List{Items: []menus.Item{
			{Label: "no audio server", Disabled: true},
		}}
	}

	sinks := st.Sinks
	if availableOnly {
		sinks = filterAvailable(sinks, st.Default)
	}
	if len(sinks) == 0 {
		return &menus.List{Items: []menus.Item{
			{Label: "no output devices", Disabled: true},
		}}
	}

	// pad every name to the same width so the percentages line up in
	// their own column at the right edge. the menu sizes itself to the
	// widest label, so that column is flush right.
	wide := 0
	for _, s := range sinks {
		if n := utf8.RuneCountInString(s.Label); n > wide {
			wide = n
		}
	}

	items := make([]menus.Item, 0, len(sinks))
	for _, s := range sinks {
		name := s.Name
		pad := strings.Repeat(" ", wide-utf8.RuneCountInString(s.Label))
		items = append(items, menus.Item{
			Label:   fmt.Sprintf("%s%s   %3d%%", s.Label, pad, int(math.Round(s.Volume))),
			Toggle:  menus.ToggleRadio,
			Checked: s.Name == st.Default,
			OnClick: func() { set(name) },
		})
	}
	return &menus.List{Items: items}
}

// filterAvailable drops devices with nothing plugged into them. The
// default sink always stays: hiding what you are listening through
// leaves a menu with no checked row. If that leaves nothing at all the
// filter is abandoned rather than showing an empty menu.
func filterAvailable(sinks []pulse.SinkInfo, def string) []pulse.SinkInfo {
	out := make([]pulse.SinkInfo, 0, len(sinks))
	for _, s := range sinks {
		if s.Available || s.Name == def {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return sinks
	}
	return out
}
