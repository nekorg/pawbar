// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ws

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/internal/services/hypr"
	"github.com/nekorg/pawbar/internal/services/i3"
	"github.com/nekorg/pawbar/pkg/module"
)

type Workspace struct {
	ID      int
	Name    string
	Active  bool
	Urgent  bool
	Special bool
}

type backend interface {
	List() []Workspace
	Events() <-chan struct{}
	Goto(name string)
	Close()
}

type wsModule struct {
	b       backend
	bname   string
	release func()

	list        []Workspace
	currentOnly bool
}

func (m *wsModule) Init(ctx *module.Ctx) error {
	if err := m.selectBackend(); err != nil {
		return err
	}
	m.list = m.b.List()
	m.refreshOpts(ctx)

	module.On(ctx, m.source(), func(list []Workspace) { m.list = list })

	ctx.HandleVerb("goto", func(a module.VerbArgs) error {
		if a.Region == "" {
			return nil
		}
		target := a.Region
		ctx.Go(func() { m.b.Goto(target) })
		return nil
	})
	return nil
}

// source adapts the backend's change signal into a workspace-list source.
func (m *wsModule) source() module.Source[[]Workspace] {
	return module.NewSource(func(emit func([]Workspace)) (module.Conn, error) {
		sig := m.b.Events()
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-sig:
					emit(m.b.List())
				case <-done:
					return
				}
			}
		}()
		var once sync.Once
		return module.ConnFuncs{
			StopFn: func() { once.Do(func() { close(done) }) },
			// Resync after suspend: workspaces may have changed while asleep.
			WakeFn: func() { emit(m.b.List()) },
		}, nil
	})
}

func (m *wsModule) selectBackend() error {
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
		m.b, m.bname, m.release = newHyprBackend(svc), "hypr", release

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
		m.b, m.bname, m.release = newI3Backend(svc), "i3", release

	default:
		return fmt.Errorf("no workspace backend for current environment")
	}
	return nil
}

func (m *wsModule) refreshOpts(ctx *module.Ctx) {
	if o, ok := ctx.Options().(*Options); ok {
		m.currentOnly = o.CurrentOnly
	}
}

func (m *wsModule) OnState(ctx *module.Ctx) {
	m.refreshOpts(ctx)
}

func (m *wsModule) Stop(ctx *module.Ctx) {
	if m.b != nil {
		m.b.Close()
	}
	if m.release != nil {
		m.release()
	}
}

func (m *wsModule) Render(w *module.Writer) {
	for _, ws := range m.list {
		if m.currentOnly && !ws.Active {
			continue
		}
		name := ws.Name
		if ws.Special {
			name = "S"
		}
		region := name
		if m.bname == "hypr" {
			region = strconv.Itoa(ws.ID)
		}
		var states []string
		if ws.Urgent {
			states = append(states, "urgent")
		}
		if ws.Active {
			states = append(states, "active")
		}
		if ws.Special {
			states = append(states, "special")
		}
		w.Text(module.P{"ws": name},
			module.States(states...), module.Region(region))
	}
}
