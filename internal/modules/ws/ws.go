// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ws

import (
	"fmt"
	"os"
	"sync"

	"github.com/nekorg/pawbar/internal/monitor"
	"github.com/nekorg/pawbar/internal/services"
	"github.com/nekorg/pawbar/internal/services/hypr"
	"github.com/nekorg/pawbar/internal/services/i3"
	"github.com/nekorg/pawbar/pkg/module"
)

type Workspace struct {
	ID   int
	Name string
	// Monitor is the output the workspace lives on, "" when the
	// compositor does not say.
	Monitor   string
	MonitorID int
	// Active is the workspace with input focus: one per session. Visible
	// is a workspace on screen somewhere, which is one per monitor.
	Active  bool
	Visible bool
	Urgent  bool
	Special bool
}

type backend interface {
	List() []Workspace
	Events() <-chan struct{}
	// Region encodes a workspace's identity into the click region the
	// module renders it with; Goto takes that encoding back.
	Region(w Workspace) string
	Goto(region string)
	Close()
}

// Monitor selection values for the `monitor` option.
const (
	monitorSelf = "self"
	monitorAll  = "all"
)

type wsModule struct {
	b       backend
	bname   string
	release func()

	list        []Workspace
	currentOnly bool
	// monitorOpt is the `monitor` option: "self", "all" or an output
	// name. self is resolved against the output this bar runs on.
	monitorOpt string
	self       string
	warnOnce   sync.Once
}

func (m *wsModule) Init(ctx *module.Ctx) error {
	if err := m.selectBackend(); err != nil {
		return err
	}
	m.self = monitor.Self()
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
	o, ok := ctx.Options().(*Options)
	if !ok {
		return
	}
	m.currentOnly = o.CurrentOnly
	m.monitorOpt = o.Monitor
	if m.monitorOpt == monitorSelf && m.self == "" {
		m.warnOnce.Do(func() {
			ctx.Log("this bar is not pinned to a monitor; showing every workspace")
		})
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

// view filters the workspace list for one bar. Order comes from the
// backend, which knows how its compositor numbers workspaces.
//
// mode is the `monitor` option: "all" shows every workspace, "self" only
// this bar's monitor, and anything else is taken as an output name. A bar
// that does not know its own output (not pinned, or a compositor that
// does not report one) shows everything rather than nothing.
func view(list []Workspace, mode, self string) []Workspace {
	want := mode
	switch mode {
	case monitorAll, "":
		return list
	case monitorSelf:
		if self == "" {
			return list
		}
		want = self
	}

	out := make([]Workspace, 0, len(list))
	for _, w := range list {
		// A compositor that does not report the monitor would filter
		// everything away; show those workspaces instead of hiding them.
		if w.Monitor == want || w.Monitor == "" {
			out = append(out, w)
		}
	}
	return out
}

func (m *wsModule) Render(w *module.Writer) {
	for _, ws := range view(m.list, m.monitorOpt, m.self) {
		// current_only keeps what is on screen: the focused workspace,
		// and on any other monitor shown the one displayed there.
		if m.currentOnly && !ws.Active && !ws.Visible {
			continue
		}
		name := ws.Name
		if ws.Special {
			name = "S"
		}
		var states []string
		if ws.Urgent {
			states = append(states, "urgent")
		}
		switch {
		case ws.Active:
			states = append(states, "active")
		case ws.Visible:
			states = append(states, "visible")
		}
		if ws.Special {
			states = append(states, "special")
		}
		w.Text(module.P{"ws": name},
			module.States(states...), module.Region(m.b.Region(ws)))
	}
}
