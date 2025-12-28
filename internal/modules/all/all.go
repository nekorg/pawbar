// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package all

import (
	"git.sr.ht/~rockorager/vaxis"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
	_ "github.com/nekorg/pawbar/internal/modules/backlight"
	_ "github.com/nekorg/pawbar/internal/modules/battery"
	_ "github.com/nekorg/pawbar/internal/modules/bluetooth"
	_ "github.com/nekorg/pawbar/internal/modules/clock"
	_ "github.com/nekorg/pawbar/internal/modules/cpu"
	_ "github.com/nekorg/pawbar/internal/modules/custom"
	_ "github.com/nekorg/pawbar/internal/modules/disk"
	_ "github.com/nekorg/pawbar/internal/modules/idleInhibitor"
	_ "github.com/nekorg/pawbar/internal/modules/locale"
	_ "github.com/nekorg/pawbar/internal/modules/mpris"
	_ "github.com/nekorg/pawbar/internal/modules/powerProfiles"
	_ "github.com/nekorg/pawbar/internal/modules/ram"
	_ "github.com/nekorg/pawbar/internal/modules/sessionControls"
	_ "github.com/nekorg/pawbar/internal/modules/title"
	_ "github.com/nekorg/pawbar/internal/modules/tray"
	_ "github.com/nekorg/pawbar/internal/modules/volume"
	_ "github.com/nekorg/pawbar/internal/modules/wifi"
	_ "github.com/nekorg/pawbar/internal/modules/ws"
	"gopkg.in/yaml.v3"
)

func init() {
	config.Register("sep", func(n *yaml.Node) (modules.Module, error) {
		return modules.NewStaticModule(
			"sep",
			[]modules.EventCell{
				{C: modules.ECSPACE.C},
				{C: vaxis.Cell{
					Character: vaxis.Character{
						Grapheme: "│",
						Width:    1,
					},
				}},
				{C: modules.ECSPACE.C},
			}, nil,
		), nil
	})

	config.Register("space", func(raw *yaml.Node) (modules.Module, error) {
		return modules.NewStaticModule(
			"space",
			[]modules.EventCell{
				{C: modules.ECSPACE.C},
			},
			nil,
		), nil
	})
}
