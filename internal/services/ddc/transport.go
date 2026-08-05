// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ddc

import (
	"fmt"

	"github.com/nekorg/pawbar/internal/logging"
)

// Display is the little a worker needs to know to reach one monitor. It is
// plain data on purpose: the sysfs walking that produces it lives in
// internal/monitor, and keeping it out of here leaves this package testable
// with no filesystem at all.
type Display struct {
	// Connector is the DRM connector name ("DP-1"), used for logging and
	// as the worker's key.
	Connector string

	// EDID identifies the display to ddcutil-service. Empty means that
	// transport cannot be used.
	EDID []byte

	// I2CBus is the /dev/i2c-N behind the connector's ddc link, or -1.
	I2CBus int
}

// transport is one way of moving VCP values to and from a display.
//
// Every timing, retry and coalescing decision lives in the worker, above
// this interface, so the two implementations stay thin and cannot drift
// apart in behaviour.
type transport interface {
	Get(vcp byte) (cur, max uint16, err error)
	Set(vcp byte, v uint16) error
	Close() error

	// Name identifies the transport in logs.
	Name() string
}

// openTransport is a variable so tests can drive a worker without hardware.
var openTransport = selectTransport

// selectTransport picks the best available way to talk to d.
//
// ddcutil-service comes first: it is a resident, D-Bus-activated daemon, so
// using it costs no process spawn, and it brings libddcutil's accumulated
// per-monitor quirk handling. Direct I2C is the fallback that keeps pawbar
// free of any hard runtime dependency.
func selectTransport(d Display) (transport, error) {
	t, dbusErr := openDBusTransport(d)
	if dbusErr == nil {
		return t, nil
	}
	logging.Log.Debug().Msgf("ddc: %s: ddcutil-service unavailable (%v); trying direct i2c",
		d.Connector, dbusErr)

	t, i2cErr := openI2CTransport(d)
	if i2cErr == nil {
		return t, nil
	}
	return nil, fmt.Errorf("no usable transport: ddcutil-service: %v; i2c: %w", dbusErr, i2cErr)
}
