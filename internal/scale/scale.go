// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package scale converts physical pixel coordinates (as reported by
// SGR-pixels mouse events) into the logical coordinates katnip panels
// are positioned with, using the scale of the monitor this bar is on.
package scale

import "github.com/nekorg/pawbar/internal/monitor"

// Factor returns this bar's monitor scale. It is not cached here: mixed
// DPI setups get a different answer per bar, and a mode switch changes it
// under a running one — monitor.Info does the caching and invalidation.
func Factor() float64 { return monitor.Scale() }

// Logical converts physical pixel coordinates to logical ones.
func Logical(x, y int) (int, int) {
	f := Factor()
	return int(float64(x) / f), int(float64(y) / f)
}
