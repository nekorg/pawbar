// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	"testing"

	"github.com/nekorg/pawbar/internal/monitor"
	"gopkg.in/yaml.v3"
)

// The topology of a laptop with an external monitor on DP-1. Note that eDP-1
// has *both* a backlight device and a ddc link — its DP AUX channel — which
// is the trap resolution ordering exists to avoid.
func laptopWithDock() ([]sysfsDevice, []monitor.Connector) {
	devs := []sysfsDevice{
		{Name: "intel_backlight", Kind: "raw", Max: 96000, Connector: "eDP-1"},
		{Name: "acpi_video0", Kind: "firmware", Max: 100},
	}
	cs := []monitor.Connector{
		{Name: "eDP-1", Connected: true, EDID: []byte("edid-edp"), I2CBus: 14, Backlight: "intel_backlight"},
		{Name: "DP-1", Connected: true, EDID: []byte("edid-dp1"), I2CBus: 15},
		{Name: "DP-2", Connected: false, I2CBus: 16},
		{Name: "HDMI-A-1", Connected: true, EDID: []byte("edid-hdmi"), I2CBus: -1},
	}
	return devs, cs
}

func TestResolve(t *testing.T) {
	devs, cs := laptopWithDock()

	for _, tc := range []struct {
		name    string
		mode    Mode
		want    string
		service bool

		mode2  Mode   // expected resolved mode
		device string // expected sysfs device
		conn   string // expected connector
	}{{
		name: "auto on the laptop panel uses sysfs, never its AUX channel",
		mode: ModeAuto, want: "eDP-1",
		mode2: ModeSysfs, device: "intel_backlight", conn: "eDP-1",
	}, {
		name: "auto on an external monitor uses ddc",
		mode: ModeAuto, want: "DP-1",
		mode2: ModeDDC, conn: "DP-1",
	}, {
		name: "auto with no pinned output falls back to sysfs",
		mode: ModeAuto, want: "",
		mode2: ModeSysfs, device: "intel_backlight",
	}, {
		name: "auto on a disconnected output falls back to sysfs",
		mode: ModeAuto, want: "DP-2",
		mode2: ModeSysfs, device: "intel_backlight",
	}, {
		name: "auto without an i2c bus still uses ddc when the service is there",
		mode: ModeAuto, want: "HDMI-A-1", service: true,
		mode2: ModeDDC, conn: "HDMI-A-1",
	}, {
		name: "auto without an i2c bus or the service falls back to sysfs",
		mode: ModeAuto, want: "HDMI-A-1",
		mode2: ModeSysfs, device: "intel_backlight",
	}, {
		name: "explicit sysfs on an external monitor uses the legacy device",
		mode: ModeSysfs, want: "DP-1",
		mode2: ModeSysfs, device: "intel_backlight",
	}, {
		name: "explicit ddc on the laptop panel is honoured",
		mode: ModeDDC, want: "eDP-1",
		mode2: ModeDDC, conn: "eDP-1",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := resolve(tc.mode, tc.want, devs, cs, tc.service)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if p.Mode != tc.mode2 {
				t.Errorf("Mode = %v, want %v", p.Mode, tc.mode2)
			}
			if tc.device != "" && p.Device.Name != tc.device {
				t.Errorf("Device = %q, want %q", p.Device.Name, tc.device)
			}
			if tc.conn != "" && p.Connector != tc.conn {
				t.Errorf("Connector = %q, want %q", p.Connector, tc.conn)
			}
			if p.Mode == ModeDDC && string(p.Display.EDID) == "" {
				t.Error("ddc plan carries no EDID to identify the display with")
			}
		})
	}
}

// The laptop-panel case deserves its own assertion: auto must not so much as
// consider the AUX bus, because probing it is what breaks real hardware.
func TestResolveNeverProbesAttachedPanel(t *testing.T) {
	devs, cs := laptopWithDock()

	p, err := resolve(ModeAuto, "eDP-1", devs, cs, true) // service available too
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ModeSysfs {
		t.Fatalf("Mode = %v, want sysfs even with ddcutil-service available", p.Mode)
	}
}

func TestResolveErrors(t *testing.T) {
	devs, cs := laptopWithDock()

	for _, tc := range []struct {
		name string
		mode Mode
		want string
		devs []sysfsDevice
	}{
		{"ddc on a disconnected output", ModeDDC, "DP-2", devs},
		{"ddc with no bus and no service", ModeDDC, "HDMI-A-1", devs},
		{"ddc on an unknown output", ModeDDC, "DP-9", devs},
		{"auto with no backlight device at all", ModeAuto, "DP-2", nil},
		{"auto with nothing anywhere", ModeAuto, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p, err := resolve(tc.mode, tc.want, tc.devs, cs, false); err == nil {
				t.Errorf("resolved to %+v, want an error", p)
			}
		})
	}
}

// "raw" is the GPU's own control; the firmware devices layered over it are
// coarser, so they are the fallback and not the default.
func TestLegacyDevicePrefersRaw(t *testing.T) {
	devs := []sysfsDevice{
		{Name: "acpi_video0", Kind: "firmware", Max: 100},
		{Name: "intel_backlight", Kind: "raw", Max: 96000},
	}
	d, ok := legacyDevice(devs)
	if !ok || d.Name != "intel_backlight" {
		t.Errorf("legacyDevice = %q (%v), want intel_backlight", d.Name, ok)
	}

	if d, ok := legacyDevice(devs[:1]); !ok || d.Name != "acpi_video0" {
		t.Errorf("with no raw device: got %q (%v), want acpi_video0", d.Name, ok)
	}
	if _, ok := legacyDevice(nil); ok {
		t.Error("legacyDevice(nil) reported a device")
	}
}

func TestModeUnmarshalYAML(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Mode
		bad  bool
	}{
		{in: "auto", want: ModeAuto},
		{in: "sysfs", want: ModeSysfs},
		{in: "ddc", want: ModeDDC},
		{in: "DDC", want: ModeDDC},
		{in: "ddc/ci", want: ModeDDC},
		{in: "sysfsx", bad: true},
		{in: "brightnessctl", bad: true},
	} {
		var got Mode
		err := yaml.Unmarshal([]byte(tc.in), &got)
		if tc.bad {
			if err == nil {
				t.Errorf("%q: accepted, want a config error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q -> %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The cascade decodes the shipped defaults and then the user's entry onto
// one struct, so a later layer has to be able to override an earlier one.
func TestModeOverridesDefault(t *testing.T) {
	m := ModeAuto
	if err := yaml.Unmarshal([]byte("sysfs"), &m); err != nil {
		t.Fatal(err)
	}
	if m != ModeSysfs {
		t.Errorf("entry layer did not override the default: got %v", m)
	}
}

func TestAttribute(t *testing.T) {
	devs := []sysfsDevice{{Name: "intel_backlight"}, {Name: "acpi_video0"}}
	cs := []monitor.Connector{{Name: "eDP-1", Backlight: "intel_backlight"}}

	got := attribute(devs, cs)
	if got[0].Connector != "eDP-1" {
		t.Errorf("intel_backlight attributed to %q, want eDP-1", got[0].Connector)
	}
	if got[1].Connector != "" {
		t.Errorf("acpi_video0 attributed to %q, want unattributed", got[1].Connector)
	}
}
