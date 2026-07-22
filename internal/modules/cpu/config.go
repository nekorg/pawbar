// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cpu

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed cpu.yaml
var defaults []byte

type Options struct {
	Tick    module.Duration `yaml:"tick"`
	HighAt  module.Percent  `yaml:"high_at"`
	HighFor module.Duration `yaml:"high_for"`
}

func init() {
	module.Register(module.Def{
		Name:    "cpu",
		Doc:     "cpu usage percentage",
		New:     func() module.Module { return &cpuModule{} },
		Options: func() any { return &Options{} },
		States: []module.StateDef{
			{Name: "high", Doc: "usage stayed above high_at for high_for"},
		},
		Placeholders: []module.Placeholder{
			{Name: "cpu", Doc: "usage percentage", Kind: module.KindNumber},
		},
		Defaults: defaults,
	})
}
