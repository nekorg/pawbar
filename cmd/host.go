// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"slices"

	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/session"
	"github.com/nekorg/pawbar/pkg/menus"
)

// hostOverrides is the kitty tuning every pawbar panel needs. In a shared
// instance this is instance-wide, which is fine because the instance is ours:
// nothing else draws in it.
//
// Panels in a shared instance cannot carry their own -o, so anything a bar or
// a menu depends on has to be here. The bar's own list and the menu host's
// used to be separate; menus need strictly more, so theirs is the base.
func hostOverrides(extra []string) []string {
	o := slices.Clone(menus.PanelOverrides)
	// Menus no longer get --config NONE in a shared instance, so the parts
	// of a user's kitty.conf that would move a panel around or make it
	// see-through are pinned here instead.
	o = append(o,
		"window_margin_width=0",
		"background_opacity=1",
	)
	return append(o, extra...)
}

// hostConfig describes the shared kitty instance.
func hostConfig(k config.KittySettings) katnip.HostConfig {
	return katnip.HostConfig{
		Socket:         session.RuntimePath("kitty"),
		ConfigFile:     k.ConfigFile(),
		KittyOverrides: hostOverrides(k.Overrides),
		// Keyed to this session so a second compositor session, or a
		// stray kitty --single-instance, never lands in our instance.
		InstanceGroup: "pawbar-" + session.Key(),
		KittyCmd:      k.Command,
	}
}
