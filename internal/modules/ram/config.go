// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ram

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed ram.yaml
var defaults []byte

type Options struct {
	Tick       module.Duration `yaml:"tick"`
	Icon       module.Icon     `yaml:"icon"`
	UseSI      bool            `yaml:"use_si"`
	Scale      module.Scale    `yaml:"unit"`
	WarnAt     module.Percent  `yaml:"warn_at"`
	CriticalAt module.Percent  `yaml:"critical_at"`
}

func init() {
	module.Register(module.Def{
		Name:    "ram",
		Doc:     "memory usage",
		New:     func() module.Module { return &ramModule{} },
		Options: func() any { return &Options{} },
		States: []module.StateDef{
			{Name: "warn", Doc: "usage at or above warn_at"},
			{Name: "critical", Doc: "usage at or above critical_at"},
		},
		Placeholders: []module.Placeholder{
			{Name: "icon", Doc: "module icon", Kind: module.KindString},
			{Name: "used", Doc: "used memory in the selected unit", Kind: module.KindNumber},
			{Name: "free", Doc: "available memory in the selected unit", Kind: module.KindNumber},
			{Name: "total", Doc: "total memory in the selected unit", Kind: module.KindNumber},
			{Name: "used_pct", Doc: "used percentage", Kind: module.KindNumber},
			{Name: "free_pct", Doc: "free percentage", Kind: module.KindNumber},
			{Name: "unit", Doc: "selected unit name", Kind: module.KindString},
		},
		Defaults: defaults,
	})
}
