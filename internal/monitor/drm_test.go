// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeTree builds a sysfs layout that mirrors what a real Intel laptop with
// a dock reports, including the two shapes a `ddc` link comes in.
func fakeTree(t *testing.T) {
	t.Helper()
	root := t.TempDir()

	drm := filepath.Join(root, "sys/class/drm")
	bl := filepath.Join(root, "sys/class/backlight")
	dev := filepath.Join(root, "dev")
	for _, d := range []string{drm, bl, dev} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}

	// Things that live in /sys/class/drm but are not connectors.
	write(filepath.Join(drm, "version"), "drm 1.1.0\n")
	if err := os.MkdirAll(filepath.Join(drm, "card1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(drm, "renderD128"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Laptop panel: connected, a ddc link *inside* the connector dir, and a
	// backlight device pointing back at it.
	write(filepath.Join(drm, "card1-eDP-1/status"), "connected\n")
	write(filepath.Join(drm, "card1-eDP-1/edid"), "\x00\xff\xffEDID-eDP")
	link("i2c-14", filepath.Join(drm, "card1-eDP-1/ddc"))

	// External display: ddc link pointing *outside* the connector dir.
	write(filepath.Join(drm, "card1-DP-1/status"), "connected\n")
	write(filepath.Join(drm, "card1-DP-1/edid"), "\x00\xff\xffEDID-DP1")
	link("../../../i2c-12", filepath.Join(drm, "card1-DP-1/ddc"))

	// Nothing plugged in: zero-length edid, no ddc link at all.
	write(filepath.Join(drm, "card1-DP-2/status"), "disconnected\n")
	write(filepath.Join(drm, "card1-DP-2/edid"), "")

	// Connected, has a ddc link, but i2c-dev never created the /dev node.
	write(filepath.Join(drm, "card1-HDMI-A-1/status"), "connected\n")
	write(filepath.Join(drm, "card1-HDMI-A-1/edid"), "\x00\xff\xffEDID-HDMI")
	link("i2c-99", filepath.Join(drm, "card1-HDMI-A-1/ddc"))

	write(filepath.Join(dev, "i2c-14"), "")
	write(filepath.Join(dev, "i2c-12"), "")

	// GPU-driven panel backlight: device resolves to the connector.
	if err := os.MkdirAll(filepath.Join(bl, "intel_backlight"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(filepath.Join(drm, "card1-eDP-1"), filepath.Join(bl, "intel_backlight/device"))

	// Firmware backlight: device resolves to an ACPI node, so it belongs to
	// no connector we can name.
	if err := os.MkdirAll(filepath.Join(root, "sys/devices/LNXVIDEO:00"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bl, "acpi_video0"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(filepath.Join(root, "sys/devices/LNXVIDEO:00"), filepath.Join(bl, "acpi_video0/device"))

	oldDRM, oldBL, oldDev := drmRoot, backlightRoot, devRoot
	drmRoot, backlightRoot, devRoot = drm, bl, dev
	t.Cleanup(func() { drmRoot, backlightRoot, devRoot = oldDRM, oldBL, oldDev })
}

func TestConnectors(t *testing.T) {
	fakeTree(t)

	cs, err := Connectors()
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Connector{}
	for _, c := range cs {
		byName[c.Name] = c
	}

	if len(cs) != 4 {
		t.Errorf("got %d connectors %v, want 4 (card1, renderD128 and version are not connectors)",
			len(cs), byName)
	}

	for _, tc := range []struct {
		name      string
		connected bool
		bus       int
		backlight string
		edid      bool
	}{
		{"eDP-1", true, 14, "intel_backlight", true},
		{"DP-1", true, 12, "", true},
		{"DP-2", false, -1, "", false},
		{"HDMI-A-1", true, -1, "", true},
	} {
		c, ok := byName[tc.name]
		if !ok {
			t.Errorf("%s: not listed", tc.name)
			continue
		}
		if c.Connected != tc.connected {
			t.Errorf("%s: Connected = %v, want %v", tc.name, c.Connected, tc.connected)
		}
		if c.I2CBus != tc.bus {
			t.Errorf("%s: I2CBus = %d, want %d", tc.name, c.I2CBus, tc.bus)
		}
		if c.Backlight != tc.backlight {
			t.Errorf("%s: Backlight = %q, want %q", tc.name, c.Backlight, tc.backlight)
		}
		if got := len(c.EDID) > 0; got != tc.edid {
			t.Errorf("%s: has EDID = %v, want %v", tc.name, got, tc.edid)
		}
		if c.HasI2C() != (tc.bus >= 0) {
			t.Errorf("%s: HasI2C = %v, want %v", tc.name, c.HasI2C(), tc.bus >= 0)
		}
	}
}

// The two ddc link shapes are the whole reason i2cBusOf uses filepath.Base;
// a driver may point inside or outside the connector directory.
func TestConnectorsDDCLinkShapes(t *testing.T) {
	fakeTree(t)

	edp, ok := ConnectorByName("eDP-1")
	if !ok {
		t.Fatal("eDP-1 missing")
	}
	dp, ok := ConnectorByName("DP-1")
	if !ok {
		t.Fatal("DP-1 missing")
	}
	if edp.I2CBus != 14 {
		t.Errorf("ddc -> i2c-14 resolved to %d, want 14", edp.I2CBus)
	}
	if dp.I2CBus != 12 {
		t.Errorf("ddc -> ../../../i2c-12 resolved to %d, want 12", dp.I2CBus)
	}
}

// A backlight whose `device` lands on an ACPI node must not be attributed to
// some arbitrary connector.
func TestConnectorsUnattributedBacklight(t *testing.T) {
	fakeTree(t)

	cs, err := Connectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Backlight == "acpi_video0" {
			t.Errorf("%s: claimed acpi_video0, which drives no known connector", c.Name)
		}
	}
}

func TestConnectorByNameMissing(t *testing.T) {
	fakeTree(t)

	if c, ok := ConnectorByName("DP-9"); ok {
		t.Errorf("ConnectorByName(DP-9) = %v, true; want not found", c)
	}
}
