// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package monitor

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DRM topology: what each connector offers to a module that wants to
// control its brightness.
//
// The kernel already answers both questions we care about, so nothing here
// has to guess. A connector directory holds a `ddc` symlink naming the I2C
// bus that display's DDC/CI lives on, and a sysfs backlight device points
// back at the connector it drives through its `device` link. Together they
// let "which backend controls this monitor?" be resolved from sysfs alone,
// with no probing and no EDID matching.

// Roots are variables so tests can point them at a temporary tree.
var (
	drmRoot       = "/sys/class/drm"
	backlightRoot = "/sys/class/backlight"
	devRoot       = "/dev"
)

// connectorDir matches a DRM connector directory: "card1-eDP-1",
// "card0-HDMI-A-1", "card1-DP-1-2". The plain card ("card1"), render nodes
// and the "version" file do not match.
var connectorDir = regexp.MustCompile(`^card\d+-(.+)$`)

// Connector is one DRM output and the brightness controls attached to it.
type Connector struct {
	// Name is the compositor-facing connector name ("eDP-1", "DP-1"),
	// the same string monitor.Self() and outputs.Monitor.Name carry.
	Name string

	// Dir is the connector's directory name ("card1-eDP-1"); Path is its
	// full path under drmRoot.
	Dir  string
	Path string

	// Connected reports the kernel's `status` for this connector.
	Connected bool

	// EDID is the raw EDID blob, empty when nothing is plugged in. It is
	// how ddcutil-service identifies a display.
	EDID []byte

	// I2CBus is the bus behind the connector's `ddc` link, or -1 when
	// there is no link or no matching /dev/i2c-N. A bus here says only
	// that a channel exists, never that a DDC/CI display answers on it:
	// laptop eDP panels expose their AUX channel the same way.
	I2CBus int

	// Backlight is the /sys/class/backlight device driving this
	// connector, or "" when no device claims it.
	Backlight string
}

// HasI2C reports whether the connector has a usable DDC channel to probe.
func (c Connector) HasI2C() bool { return c.I2CBus >= 0 }

// Connectors lists every DRM connector on the system.
func Connectors() ([]Connector, error) {
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return nil, err
	}

	backlights := backlightsByConnector()

	var out []Connector
	for _, e := range entries {
		m := connectorDir.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		dir := filepath.Join(drmRoot, e.Name())
		c := Connector{
			Name:      m[1],
			Dir:       e.Name(),
			Path:      dir,
			Connected: readTrimmed(filepath.Join(dir, "status")) == "connected",
			I2CBus:    i2cBusOf(dir),
			Backlight: backlights[e.Name()],
		}
		// A disconnected connector reports a zero-length edid rather than
		// an error, so an empty blob is normal and not worth reporting.
		if blob, err := os.ReadFile(filepath.Join(dir, "edid")); err == nil && len(blob) > 0 {
			c.EDID = blob
		}
		out = append(out, c)
	}
	return out, nil
}

// ConnectorByName returns the named connector ("eDP-1", "DP-1").
func ConnectorByName(name string) (Connector, bool) {
	cs, err := Connectors()
	if err != nil {
		return Connector{}, false
	}
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return Connector{}, false
}

// i2cBusOf resolves a connector's `ddc` link to an I2C bus number, or -1.
//
// The link target's shape varies with the driver — "i2c-15" for one
// connector, "../../../i2c-12" for another on the same card — so only the
// base name is dependable. A bus whose /dev node is missing counts as
// absent: that is what an unloaded i2c-dev module looks like.
func i2cBusOf(dir string) int {
	target, err := os.Readlink(filepath.Join(dir, "ddc"))
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(target), "i2c-"))
	if err != nil || n < 0 {
		return -1
	}
	if _, err := os.Stat(filepath.Join(devRoot, "i2c-"+strconv.Itoa(n))); err != nil {
		return -1
	}
	return n
}

// backlightsByConnector maps a connector directory name to the backlight
// device driving it.
//
// A backlight's `device` link resolves to the DRM connector for panels the
// GPU drives directly (intel_backlight -> card1-eDP-1). Firmware backlights
// (acpi_video0) and vendor ones resolve to an ACPI or PCI node instead;
// those stay unattributed, which is the honest answer — we know they exist
// but not which output they light.
func backlightsByConnector() map[string]string {
	entries, err := os.ReadDir(backlightRoot)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		dev, err := filepath.EvalSymlinks(filepath.Join(backlightRoot, e.Name(), "device"))
		if err != nil {
			continue
		}
		if base := filepath.Base(dev); connectorDir.MatchString(base) {
			// First writer wins; two backlights on one connector is not
			// a case the kernel produces.
			if _, dup := out[base]; !dup {
				out[base] = e.Name()
			}
		}
	}
	return out
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
