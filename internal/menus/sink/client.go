// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package sink

import (
	"fmt"
	"math"

	"github.com/nekorg/pawbar/internal/services/pulse"
	"github.com/nekorg/pawbar/pkg/menus"
)

// Menu builds the output-device picker from a state snapshot. set
// switches the default sink; the caller's own subscription is what
// brings the bar back in sync afterwards.
func Menu(st pulse.State, set func(name string) error) *menus.List {
	if !st.Connected {
		return &menus.List{Items: []menus.Item{
			{Label: "no audio server", Disabled: true},
		}}
	}
	if len(st.Sinks) == 0 {
		return &menus.List{Items: []menus.Item{
			{Label: "no output devices", Disabled: true},
		}}
	}

	items := make([]menus.Item, 0, len(st.Sinks))
	for _, s := range st.Sinks {
		name := s.Name
		items = append(items, menus.Item{
			Label:   fmt.Sprintf("%s  %d%%", s.Label(), int(math.Round(s.Volume))),
			Toggle:  menus.ToggleRadio,
			Checked: s.Name == st.Default,
			OnClick: func() { set(name) },
		})
	}
	return &menus.List{Items: items}
}
