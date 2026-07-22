// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jochenvg/go-udev"
	"github.com/nekorg/pawbar/internal/utils"
	"github.com/nekorg/pawbar/pkg/module"
)

type backlightModule struct {
	opts *Options

	device string
	now    int
	max    int
}

func (m *backlightModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	if err := m.pickDevice(); err != nil {
		return err
	}
	m.read(ctx)
	module.On(ctx, udevSource(), func(struct{}) { m.read(ctx) })
	return nil
}

func (m *backlightModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
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
			for {
				select {
				case d, ok := <-devChan:
					if !ok || d == nil {
						return
					}
					emit(struct{}{})
				case e := <-errChan:
					if e != nil {
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

func (m *backlightModule) pickDevice() error {
	basePath := "/sys/class/backlight/"
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return err
	}

	type device struct {
		name    string
		devType string
		max     int
	}
	var valid []device
	for _, entry := range entries {
		devicePath := filepath.Join(basePath, entry.Name())
		typeData, err := os.ReadFile(filepath.Join(devicePath, "type"))
		if err != nil {
			continue
		}
		maxData, err := os.ReadFile(filepath.Join(devicePath, "max_brightness"))
		if err != nil {
			continue
		}
		max, err := strconv.Atoi(strings.TrimSpace(string(maxData)))
		if err != nil || max == 0 {
			continue
		}
		valid = append(valid, device{entry.Name(), strings.TrimSpace(string(typeData)), max})
	}
	if len(valid) == 0 {
		return fmt.Errorf("no valid backlight devices found")
	}

	selected := valid[0]
	for _, d := range valid {
		if d.devType == "raw" {
			selected = d
			break
		}
	}
	m.device, m.max = selected.name, selected.max
	return nil
}

func (m *backlightModule) read(ctx *module.Ctx) {
	data, err := os.ReadFile(filepath.Join("/sys/class/backlight", m.device, "brightness"))
	if err != nil {
		ctx.Log("read brightness: %v", err)
		return
	}
	now, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		ctx.Log("parse brightness: %v", err)
		return
	}
	m.now = now
}

func (m *backlightModule) Render(w *module.Writer) {
	if m.max == 0 {
		return
	}
	pct := m.now * 100 / m.max
	icon := ""
	if len(m.opts.Icons) > 0 {
		icon = m.opts.Icons[utils.Clamp(pct*len(m.opts.Icons)/100, 0, len(m.opts.Icons)-1)]
	}
	w.Text(module.P{"icon": icon, "light": pct, "now": m.now, "max": m.max})
}
