// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package main

import (
	"flag"

	"github.com/nekorg/pawbar/pkg/dbusmenukitty"
)

func main() {
	var x, y int
	var service, path string

	flag.StringVar(&service, "service", "org.freedesktop.network-manager-applet", "DBus service name exposing a dbusmenu")
	flag.StringVar(&path, "path", "/org/ayatana/NotificationItem/nm_applet/Menu", "DBus object path of the menu")
	flag.IntVar(&x, "x", 0, "X coordinate for panel (physical pixels)")
	flag.IntVar(&y, "y", 0, "Y coordinate for panel (physical pixels)")
	flag.Parse()

	// LaunchMenu will not return until the panel closes (or an error occurs).
	dbusmenukitty.LaunchMenu(service, path, x, y)
}
