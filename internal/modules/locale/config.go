// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package locale

import (
	_ "embed"

	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed locale.yaml
var defaults []byte

type Options struct {
	Tick module.Duration `yaml:"tick"`
}

func init() {
	module.Register(module.Def{
		Name:    "locale",
		Doc:     "current locale from the environment",
		New:     func() module.Module { return &localeModule{} },
		Options: func() any { return &Options{} },
		Placeholders: []module.Placeholder{
			{Name: "locale", Doc: "language-REGION, e.g. en-US", Kind: module.KindString},
		},
		Defaults: defaults,
	})
}
