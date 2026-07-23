// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package scale converts physical pixel coordinates (as reported by
// SGR-pixels mouse events) into the logical coordinates katnip panels
// are positioned with, using the primary monitor's scale.
package scale

import (
	"sync"

	"github.com/codelif/outputs"
	"github.com/nekorg/pawbar/internal/logging"
)

var (
	once sync.Once
	// factor falls back to the historical assumption of a 2x display
	// when the monitor query fails.
	factor = 2.0
)

// Factor returns the primary monitor's scale, queried once per process.
func Factor() float64 {
	once.Do(func() {
		monitors, err := outputs.GetMonitors()
		if err != nil || len(monitors) == 0 {
			logging.Log.Warn().Msgf("scale: monitor query failed (%v); assuming 2x", err)
			return
		}
		m := monitors[0]
		for _, mon := range monitors {
			if mon.IsPrimary {
				m = mon
				break
			}
		}
		if m.Scale > 0 {
			factor = m.Scale
		}
	})
	return factor
}

// Logical converts physical pixel coordinates to logical ones.
func Logical(x, y int) (int, int) {
	f := Factor()
	return int(float64(x) / f), int(float64(y) / f)
}
