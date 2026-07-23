// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package title

import (
	"fmt"
	"os"

	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/internal/services/hypr"
	"github.com/nekorg/pawbar/internal/services/i3"
	"github.com/nekorg/pawbar/pkg/module"
)

type Window struct {
	Title string
	Class string
}

type backend interface {
	Window() Window
	Events() <-chan struct{}
	Close()
}

type titleModule struct {
	b       backend
	release func()
	win     Window
}

func (m *titleModule) Init(ctx *module.Ctx) error {
	if err := m.selectBackend(); err != nil {
		return err
	}
	m.win = m.b.Window()

	module.On(ctx, m.source(), func(w Window) { m.win = w })
	return nil
}

func (m *titleModule) source() module.Source[Window] {
	return module.NewSource(func(emit func(Window)) (module.Conn, error) {
		sig := m.b.Events()
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-sig:
					emit(m.b.Window())
				case <-done:
					return
				}
			}
		}()
		return module.ConnFuncs{
			StopFn: func() { close(done) },
			WakeFn: func() { emit(m.b.Window()) },
		}, nil
	})
}

func (m *titleModule) selectBackend() error {
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		svc, release, err := services.Acquire("hypr", func() (*hypr.Service, error) {
			s := &hypr.Service{}
			if err := s.Start(); err != nil {
				return nil, err
			}
			return s, nil
		})
		if err != nil {
			return fmt.Errorf("hypr service: %w", err)
		}
		m.b, m.release = newHyprBackend(svc), release

	case os.Getenv("I3SOCK") != "" || os.Getenv("SWAYSOCK") != "":
		svc, release, err := services.Acquire("i3", func() (*i3.Service, error) {
			s := &i3.Service{}
			if err := s.Start(); err != nil {
				return nil, err
			}
			return s, nil
		})
		if err != nil {
			return fmt.Errorf("i3 service: %w", err)
		}
		m.b, m.release = newI3Backend(svc), release

	default:
		return fmt.Errorf("no window backend for current environment")
	}
	return nil
}

func (m *titleModule) Stop(ctx *module.Ctx) {
	if m.b != nil {
		m.b.Close()
	}
	if m.release != nil {
		m.release()
	}
}

func (m *titleModule) Render(w *module.Writer) {
	data := module.P{"title": m.win.Title, "class": m.win.Class}
	if m.win.Class != "" {
		w.Text(data, module.States("class"))
	}
	if m.win.Title != "" && m.win.Class != "" {
		w.Raw(" ")
		w.Text(data)
	}
}
