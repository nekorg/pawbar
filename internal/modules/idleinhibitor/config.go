// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package idleinhibitor

import (
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
)

func init() {
	config.RegisterModule("idleinhibitor", defaultOptions, func(o Options) (modules.Module, error) { return &IdleModule{opts: o}, nil })
}

type inhibitOptions struct {
	Fg     config.Color  `yaml:"fg"`
	Bg     config.Color  `yaml:"bg"`
	Format config.Format `yaml:"format"`
}

type Options struct {
	Fg      config.Color                      `yaml:"fg"`
	Bg      config.Color                      `yaml:"bg"`
	Cursor  config.Cursor                     `yaml:"cursor"`
	Format  config.Format                     `yaml:"format"`
	Inhibit inhibitOptions                    `yaml:"inhibit"`
	OnClick config.MouseActions[MouseOptions] `yaml:"onmouse"`
}

type MouseOptions struct {
	Fg     *config.Color  `yaml:"fg"`
	Bg     *config.Color  `yaml:"bg"`
	Cursor *config.Cursor `yaml:"cursor"`
	Format *config.Format `yaml:"format"`
}

func defaultOptions() Options {
	fn, _ := config.NewTemplate("")
	f, _ := config.NewTemplate("")
	return Options{
		Inhibit: inhibitOptions{
			Format: config.Format{Template: fn},
		},
		Format:  config.Format{Template: f},
		OnClick: config.MouseActions[MouseOptions]{},
	}
}
