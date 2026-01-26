// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package clock

import (
	"time"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
)

// Example config:
//
//  clock:
//     format: "%Y-%m-%d %H:%M:%S"
//     auto_tick: true                            # derive tick from format
//     tick:   5s                                   # interval
//     onmouse:
//       left:
//         config:
//           format: "%a %H:%M"
//       right:
//         config:
//           format: "%d %B %Y (%A) %H:%M"
//
// NOTE: include an example in every module's config.go (also this message)

func init() {
	config.RegisterModule("clock", defaultOptions, func(o Options) (modules.Module, error) { return &ClockModule{opts: o}, nil })
}

type Options struct {
	Fg       config.Color                      `yaml:"fg"`
	Bg       config.Color                      `yaml:"bg"`
	Cursor   config.Cursor                     `yaml:"cursor"`
	Tick     config.Duration                   `yaml:"tick"`
	AutoTick bool                              `yaml:"auto_tick"`
	Format   string                            `yaml:"format"`
	OnClick  config.MouseActions[MouseOptions] `yaml:"onmouse"`
}

type MouseOptions struct {
	Fg     *config.Color    `yaml:"fg"`
	Bg     *config.Color    `yaml:"bg"`
	Cursor *config.Cursor   `yaml:"cursor"`
	Tick   *config.Duration `yaml:"tick"`
	Format *string          `yaml:"format"`
}

func defaultOptions() Options {
	return Options{
		Format:   "%Y-%m-%d %H:%M:%S",
		Tick:     config.Duration(5 * time.Second),
		AutoTick: true,
		OnClick:  config.MouseActions[MouseOptions]{},
	}
}
