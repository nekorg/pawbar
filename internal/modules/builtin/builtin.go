// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package builtin registers every built-in module with the SDK registry.
// The main package blank-imports it; ported module packages get added to
// the import list below as the migration progresses.
package builtin

import (
	_ "github.com/nekorg/pawbar/internal/modules/backlight"
	_ "github.com/nekorg/pawbar/internal/modules/battery"
	_ "github.com/nekorg/pawbar/internal/modules/bluetooth"
	_ "github.com/nekorg/pawbar/internal/modules/clock"
	_ "github.com/nekorg/pawbar/internal/modules/cpu"
	_ "github.com/nekorg/pawbar/internal/modules/custom"
	_ "github.com/nekorg/pawbar/internal/modules/disk"
	_ "github.com/nekorg/pawbar/internal/modules/idleinhibitor"
	_ "github.com/nekorg/pawbar/internal/modules/locale"
	_ "github.com/nekorg/pawbar/internal/modules/mpris"
	_ "github.com/nekorg/pawbar/internal/modules/powerprofiles"
	_ "github.com/nekorg/pawbar/internal/modules/ram"
	_ "github.com/nekorg/pawbar/internal/modules/sessioncontrols"
	_ "github.com/nekorg/pawbar/internal/modules/title"
	_ "github.com/nekorg/pawbar/internal/modules/tray"
	_ "github.com/nekorg/pawbar/internal/modules/volume"
	_ "github.com/nekorg/pawbar/internal/modules/wifi"
	_ "github.com/nekorg/pawbar/internal/modules/ws"
	"github.com/nekorg/pawbar/pkg/module"
)

func init() {
	module.Register(module.Static("sep", " │ "))
	module.Register(module.Static("space", " "))
}
