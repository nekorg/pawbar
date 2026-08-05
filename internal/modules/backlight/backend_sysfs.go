// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/godbus/dbus/v5"
	"github.com/jochenvg/go-udev"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/module"
)

// The kernel backlight class: internal panels.
//
// Reading is a file read. Writing is not, because /sys/class/backlight is
// root:video and no ACL grants the logged-in user access — so the write goes
// through logind, which lets any active session set its own backlight
// without a group, a udev rule or a polkit prompt.

type sysfsBackend struct {
	dev sysfsDevice
	now int

	// writer is the write path that worked last time. The first write
	// discovers it; every one after that is a single call.
	writer func(raw int) error
}

func newSysfsBackend(d sysfsDevice) *sysfsBackend { return &sysfsBackend{dev: d} }

func (b *sysfsBackend) Name() string { return "sysfs" }

func (b *sysfsBackend) Raw() (now, max int) { return b.now, b.dev.Max }

func (b *sysfsBackend) Pct() int {
	if b.dev.Max == 0 {
		return 0
	}
	return (b.now*100 + b.dev.Max/2) / b.dev.Max
}

func (b *sysfsBackend) Start(ctx *module.Ctx) error {
	b.read(ctx)
	module.On(ctx, udevSource(), func(struct{}) { b.read(ctx) })
	return nil
}

func (b *sysfsBackend) Stop() {}

func (b *sysfsBackend) Set(pct int) error {
	raw := (pct*b.dev.Max + 50) / 100
	if raw < 0 {
		raw = 0
	}
	if raw > b.dev.Max {
		raw = b.dev.Max
	}

	if b.writer != nil {
		if err := b.writer(raw); err != nil {
			return err
		}
		b.now = raw
		return nil
	}

	var errs []error
	for _, w := range []struct {
		name string
		fn   func(int) error
	}{
		{"logind", b.setViaLogind},
		{"sysfs", b.setViaSysfs},
		{"brightnessctl", b.setViaBrightnessctl},
	} {
		if err := w.fn(raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", w.name, err))
			continue
		}
		logging.Log.Debug().Msgf("backlight: writing brightness via %s", w.name)
		b.writer = w.fn
		b.now = raw
		return nil
	}
	return fmt.Errorf("no way to set brightness: %w", errors.Join(errs...))
}

func (b *sysfsBackend) path() string {
	return filepath.Join(backlightRoot, b.dev.Name, "brightness")
}

func (b *sysfsBackend) read(ctx *module.Ctx) {
	now, err := readInt(b.path())
	if err != nil {
		ctx.Log("read brightness: %v", err)
		return
	}
	b.now = now
}

// setViaLogind asks logind to write the file for us. It permits any active
// session to set its own seat's backlight, so this needs no group
// membership, no udev rule and raises no polkit prompt.
func (b *sysfsBackend) setViaLogind(raw int) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}
	defer conn.Close()

	// The "auto" alias resolves to the caller's own session, which beats
	// reading XDG_SESSION_ID: that is unset in plenty of session setups.
	obj := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1/session/auto")
	call := obj.Call("org.freedesktop.login1.Session.SetBrightness", 0,
		"backlight", b.dev.Name, uint32(raw))
	return call.Err
}

// setViaSysfs writes the file directly, which works on non-systemd systems
// where the user is in the `video` group.
func (b *sysfsBackend) setViaSysfs(raw int) error {
	return os.WriteFile(b.path(), []byte(strconv.Itoa(raw)), 0o644)
}

// setViaBrightnessctl is the last resort: it covers a setuid brightnessctl
// on a system with neither logind nor `video` membership.
func (b *sysfsBackend) setViaBrightnessctl(raw int) error {
	path, err := exec.LookPath("brightnessctl")
	if err != nil {
		return err
	}
	cmd := exec.Command(path, "--device", b.dev.Name, "set", strconv.Itoa(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// udevSource emits a signal for every backlight udev event.
func udevSource() module.Source[struct{}] {
	return module.NewSource(func(emit func(struct{})) (module.Conn, error) {
		u := udev.Udev{}
		monitor := u.NewMonitorFromNetlink("udev")
		if err := monitor.FilterAddMatchSubsystem("backlight"); err != nil {
			return nil, err
		}
		cctx, cancel := context.WithCancel(context.Background())
		devChan, errChan, err := monitor.DeviceChan(cctx)
		if err != nil || devChan == nil || errChan == nil {
			cancel()
			if err == nil {
				err = fmt.Errorf("failed to initialize backlight udev monitor")
			}
			return nil, err
		}
		go func() {
			defer logging.Recover("backlight.udev")
			for {
				select {
				case d, ok := <-devChan:
					if !ok || d == nil {
						logging.Log.Warn().Msg("backlight: udev monitor closed; live brightness updates stopped")
						return
					}
					emit(struct{}{})
				case e := <-errChan:
					if e != nil {
						logging.Log.Warn().Msgf("backlight: udev monitor: %v; live brightness updates stopped", e)
						return
					}
				case <-cctx.Done():
					return
				}
			}
		}()
		return module.ConnFuncs{StopFn: cancel}, nil
	})
}
