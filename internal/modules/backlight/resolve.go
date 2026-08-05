// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nekorg/pawbar/internal/monitor"
	"github.com/nekorg/pawbar/internal/services/ddc"
	"gopkg.in/yaml.v3"
)

// Mode selects how brightness is read and written.
type Mode uint8

const (
	// ModeAuto detects the right backend for this bar's monitor.
	ModeAuto Mode = iota
	// ModeSysfs is the kernel backlight class: internal panels.
	ModeSysfs
	// ModeDDC is DDC/CI over I2C: external monitors.
	ModeDDC
)

func (m *Mode) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	switch strings.ToLower(raw) {
	case "auto", "":
		*m = ModeAuto
	case "sysfs":
		*m = ModeSysfs
	case "ddc", "ddc/ci", "ddcci":
		*m = ModeDDC
	default:
		return fmt.Errorf("%q is not a valid backend. valid options are [%q, %q, %q]",
			raw, "auto", "sysfs", "ddc")
	}
	return nil
}

func (m Mode) String() string {
	switch m {
	case ModeSysfs:
		return "sysfs"
	case ModeDDC:
		return "ddc"
	default:
		return "auto"
	}
}

func (m Mode) MarshalYAML() (any, error) { return m.String(), nil }

// sysfsDevice is one usable /sys/class/backlight device.
type sysfsDevice struct {
	Name string
	Kind string // "raw", "firmware", "platform"
	Max  int

	// Connector is the DRM output this device lights, or "" when the
	// kernel does not say (firmware and vendor backlights).
	Connector string
}

// plan is what resolution decided.
type plan struct {
	Mode Mode

	// Connector is the output this plan controls, "" for a legacy
	// unattributed sysfs device.
	Connector string

	Device  sysfsDevice // when Mode == ModeSysfs
	Display ddc.Display // when Mode == ModeDDC
}

// resolve decides which backend controls `want`.
//
// Kept pure over its inputs so the ordering below — which is the whole
// substance of `auto` — can be tested without hardware.
//
// The ordering matters more than any single check. sysfs is tried before
// DDC because a laptop panel is exactly the case that has both: eDP-1 owns
// a backlight device *and* exposes its AUX channel as a `ddc` link, so
// probing first would talk to the wrong thing on the most common hardware
// there is.
func resolve(mode Mode, want string, devs []sysfsDevice, cs []monitor.Connector, service bool) (plan, error) {
	var conn *monitor.Connector
	for i := range cs {
		if cs[i].Name == want {
			conn = &cs[i]
			break
		}
	}
	if want != "" && conn == nil && mode == ModeDDC {
		return plan{}, fmt.Errorf("output %q is not a known DRM connector", want)
	}

	// 1. A backlight device attributed to this output wins outright.
	if conn != nil && mode != ModeDDC {
		if d, ok := deviceNamed(devs, conn.Backlight); ok {
			return plan{Mode: ModeSysfs, Connector: conn.Name, Device: d}, nil
		}
	}

	// 2. DDC/CI, for an output that is actually plugged in and reachable.
	if conn != nil && mode != ModeSysfs {
		switch {
		case !conn.Connected:
			if mode == ModeDDC {
				return plan{}, fmt.Errorf("%s is not connected", conn.Name)
			}
		case len(conn.EDID) == 0:
			if mode == ModeDDC {
				return plan{}, fmt.Errorf("%s reports no EDID", conn.Name)
			}
		case !conn.HasI2C() && !service:
			if mode == ModeDDC {
				return plan{}, fmt.Errorf(
					"%s has no i2c bus (is the i2c-dev module loaded?) and ddcutil-service is not available",
					conn.Name)
			}
		default:
			return plan{
				Mode:      ModeDDC,
				Connector: conn.Name,
				Display: ddc.Display{
					Connector: conn.Name,
					EDID:      conn.EDID,
					I2CBus:    conn.I2CBus,
				},
			}, nil
		}
	}
	if mode == ModeDDC {
		return plan{}, fmt.Errorf("no DDC/CI display for output %q", want)
	}

	// 3. Any usable backlight device, attributed or not. This is what pawbar
	//    did before it knew about outputs, and it keeps every existing
	//    single-monitor config working exactly as it did.
	if d, ok := legacyDevice(devs); ok {
		return plan{Mode: ModeSysfs, Device: d}, nil
	}

	if want == "" {
		return plan{}, fmt.Errorf("no usable backlight device")
	}
	return plan{}, fmt.Errorf("no brightness control for output %q", want)
}

func deviceNamed(devs []sysfsDevice, name string) (sysfsDevice, bool) {
	if name == "" {
		return sysfsDevice{}, false
	}
	for _, d := range devs {
		if d.Name == name {
			return d, true
		}
	}
	return sysfsDevice{}, false
}

// legacyDevice prefers a "raw" device, matching what pawbar has always
// picked: raw is the GPU's own control, and the firmware ones layered over
// it tend to be coarser.
func legacyDevice(devs []sysfsDevice) (sysfsDevice, bool) {
	if len(devs) == 0 {
		return sysfsDevice{}, false
	}
	for _, d := range devs {
		if d.Kind == "raw" {
			return d, true
		}
	}
	return devs[0], true
}

// backlightRoot is a variable so tests can point it at a temporary tree.
var backlightRoot = "/sys/class/backlight"

// scanSysfs lists every backlight device the kernel exposes, dropping the
// ones that cannot answer for themselves.
func scanSysfs() []sysfsDevice {
	entries, err := os.ReadDir(backlightRoot)
	if err != nil {
		return nil
	}
	var out []sysfsDevice
	for _, e := range entries {
		dir := filepath.Join(backlightRoot, e.Name())
		max, err := readInt(filepath.Join(dir, "max_brightness"))
		if err != nil || max == 0 {
			continue
		}
		kind, err := os.ReadFile(filepath.Join(dir, "type"))
		if err != nil {
			continue
		}
		out = append(out, sysfsDevice{
			Name: e.Name(),
			Kind: strings.TrimSpace(string(kind)),
			Max:  max,
		})
	}
	return out
}

// attribute fills in each device's Connector from the DRM topology.
func attribute(devs []sysfsDevice, cs []monitor.Connector) []sysfsDevice {
	for i := range devs {
		for _, c := range cs {
			if c.Backlight == devs[i].Name {
				devs[i].Connector = c.Name
				break
			}
		}
	}
	return devs
}

func readInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}
