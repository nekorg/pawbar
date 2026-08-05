// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package monitor answers "which output is this bar on?".
//
// The answer is declared, never inferred: the supervisor pins every panel
// to an output with kitty's --output-name and passes the same name down in
// PAWBAR_OUTPUT, so a panel process (and every helper it spawns, which
// inherit the environment) knows its monitor for certain.
package monitor

import (
	"os"
	"sync"
	"time"

	"github.com/codelif/outputs"
	"github.com/nekorg/pawbar/internal/logging"
)

// EnvOutput carries the panel's output name from the supervisor to the
// panel process.
const EnvOutput = "PAWBAR_OUTPUT"

// cacheTTL bounds how stale Info's answer can be. Monitor geometry only
// changes on hotplug or a mode switch, and Invalidate covers the changes
// the bar itself observes.
const cacheTTL = 3 * time.Second

// Self returns the output this process's bar was pinned to, or "" when it
// was not pinned (a standalone helper binary, or a panel started outside
// the supervisor).
func Self() string { return os.Getenv(EnvOutput) }

var cache struct {
	mu     sync.Mutex
	mon    outputs.Monitor
	ok     bool
	when   time.Time
	warned bool
}

// Invalidate drops the cached geometry. The bar calls this when the panel
// is resized, which is how a scale or mode change announces itself.
func Invalidate() {
	cache.mu.Lock()
	cache.when = time.Time{}
	cache.mu.Unlock()
}

// Info returns this bar's monitor. Without a pinned output (or when the
// pinned one is not connected) it falls back to the primary monitor, which
// is the best guess available and matches pawbar's pre-multi-monitor
// behaviour.
func Info() (outputs.Monitor, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.ok && time.Since(cache.when) < cacheTTL {
		return cache.mon, true
	}

	monitors, err := outputs.GetMonitors()
	if err != nil || len(monitors) == 0 {
		if !cache.warned {
			cache.warned = true
			logging.Log.Warn().Msgf("monitor: query failed (%v); geometry unknown", err)
		}
		// A stale answer beats none: the bar is still on the same output.
		return cache.mon, cache.ok
	}

	self := Self()
	if self != "" {
		for _, m := range monitors {
			if m.Name == self {
				cache.mon, cache.ok, cache.when = m, true, time.Now()
				return m, true
			}
		}
		if !cache.warned {
			cache.warned = true
			logging.Log.Warn().Msgf("monitor: %s is not connected; falling back to the primary monitor", self)
		}
	}

	m := monitors[0]
	for _, mon := range monitors {
		if mon.IsPrimary {
			m = mon
			break
		}
	}
	cache.mon, cache.ok, cache.when = m, true, time.Now()
	return m, true
}

// Scale returns this bar's monitor scale, falling back to the historical
// assumption of a 2x display when the monitor is unknown.
func Scale() float64 {
	if m, ok := Info(); ok && m.Scale > 0 {
		return m.Scale
	}
	return 2.0
}
