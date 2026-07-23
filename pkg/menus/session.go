// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"errors"
	"io"
	l "log"
	"os"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
	"go.rockorager.dev/vaxis"
)

// AppFunc is the child-side entry point of a menu: it runs inside the
// menu's kitty process with the boilerplate (vaxis, wire decoding,
// Esc/close handling, focus reporting) already wired up. It should
// consume s.Events() until the channel closes, then return.
type AppFunc func(s *Session) int

// Register wires a menu app into katnip's re-exec dispatch. Call it
// from an init() of a package linked into the binary.
func Register(name string, app AppFunc) {
	katnip.RegisterFunc(name, func(k *katnip.Kitty, rw io.ReadWriter) int {
		logging.SetupFileOnly(name)
		return runSession(k, rw, app)
	})
}

// Session is a menu app's handle to its panel: the vaxis screen, the
// filtered event stream and the wire channel back to the bar.
type Session struct {
	vx    *vaxis.Vaxis
	k     *katnip.Kitty
	enc   *cbor.Encoder
	encMu sync.Mutex

	events chan vaxis.Event
	msgs   chan wire.Msg

	geoMu sync.Mutex
	geo   wire.Geometry

	szMu             sync.Mutex
	cols, rows       int
	xPixels, yPixels int
}

func (s *Session) setSize(cols, rows, xPixels, yPixels int) {
	if cols <= 0 || rows <= 0 || xPixels <= 0 || yPixels <= 0 {
		return
	}
	s.szMu.Lock()
	s.cols, s.rows = cols, rows
	s.xPixels, s.yPixels = xPixels, yPixels
	s.szMu.Unlock()
}

// MeasuredPPC returns the panel's own physical pixels-per-cell, as
// reported by its terminal — the ground truth for pixel-exact
// placement relative to this panel's rows. Zero when unknown.
func (s *Session) MeasuredPPC() (float64, float64) {
	s.szMu.Lock()
	defer s.szMu.Unlock()
	if s.cols <= 0 || s.rows <= 0 {
		return 0, 0
	}
	return float64(s.xPixels) / float64(s.cols), float64(s.yPixels) / float64(s.rows)
}

// Vx exposes the underlying vaxis instance (graphics, color queries).
func (s *Session) Vx() *vaxis.Vaxis { return s.vx }

// Window returns the drawing surface.
func (s *Session) Window() vaxis.Window { return s.vx.Window() }

// Render flushes the current frame.
func (s *Session) Render() { s.vx.Render() }

// Events streams input and screen events. The channel closes when the
// menu must exit (Esc, a close message from the bar); return from the
// app when that happens. Esc and focus events are handled by the host
// and never appear here.
func (s *Session) Events() <-chan vaxis.Event { return s.events }

// Messages streams non-lifecycle wire messages from the bar (e.g. item
// updates). Apps that don't consume it lose messages rather than
// wedging the host.
func (s *Session) Messages() <-chan wire.Msg { return s.msgs }

// Send delivers a message to the bar process.
func (s *Session) Send(m wire.Msg) error {
	if s.enc == nil {
		return errors.New("menus: no wire channel")
	}
	s.encMu.Lock()
	defer s.encMu.Unlock()
	return s.enc.Encode(m)
}

// Geometry returns the placement data shipped by the bar, for
// child-side re-clamping.
func (s *Session) Geometry() wire.Geometry {
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	return s.geo
}

func (s *Session) setGeometry(g wire.Geometry) {
	s.geoMu.Lock()
	s.geo = g
	s.geoMu.Unlock()
}

// Resize grows or shrinks the panel to cols x rows, moving it first if
// the new size would clip off the monitor. It reports the resulting
// placement back to the bar so submenu math stays accurate.
func (s *Session) Resize(cols, rows int) {
	geo := s.Geometry()
	if geo.MonW > 0 && geo.Scale > 0 {
		w := cellsToLogical(cols, geo.PPCX, geo.Scale) + 2*geo.Pad
		h := cellsToLogical(rows, geo.PPCY, geo.Scale) + 2*geo.Pad
		x := clamp(geo.PanelX, 0, geo.MonW-w)
		y := clamp(geo.PanelY, 0, geo.MonH-h)
		if x != geo.PanelX || y != geo.PanelY {
			s.k.Move(x, y)
			geo.PanelX, geo.PanelY = x, y
			s.setGeometry(geo)
		}
	}
	s.k.Resize(cols, rows)
	s.Send(wire.Msg{Type: wire.MsgResized, Cols: cols, Rows: rows, Geo: &geo})
}

// runSession owns the per-menu boilerplate every menu used to
// hand-roll: vaxis setup, wire decoding, Esc-to-close, focus
// reporting, and close-on-command.
func runSession(k *katnip.Kitty, rw io.ReadWriter, app AppFunc) int {
	l.SetOutput(io.Discard)

	vx, err := vaxis.New(vaxis.Options{
		WithTTY:         os.Stdout.Name(),
		EnableSGRPixels: true,
	})
	if err != nil {
		return 1
	}
	defer vx.Close()

	s := &Session{
		vx:     vx,
		k:      k,
		events: make(chan vaxis.Event, 32),
		msgs:   make(chan wire.Msg, 16),
	}
	{
		ws := vx.Size()
		s.setSize(ws.Cols, ws.Rows, ws.XPixel, ws.YPixel)
	}

	ctrl := make(chan wire.Msg, 16)
	if rw != nil {
		s.enc = cbor.NewEncoder(rw)
		go func() {
			defer close(ctrl)
			dec := cbor.NewDecoder(rw)
			for {
				var m wire.Msg
				if err := dec.Decode(&m); err != nil {
					return
				}
				ctrl <- m
			}
		}()
	}

	k.Show()

	// The host loop is the only sender on s.events and the only party
	// that closes it; the app exits by draining the closed channel.
	go func() {
		defer close(s.events)
		focused := false
		for {
			select {
			case ev := <-vx.Events():
				switch ev := ev.(type) {
				case vaxis.Resize:
					s.setSize(ev.Cols, ev.Rows, ev.XPixel, ev.YPixel)
				case vaxis.Key:
					if ev.EventType == vaxis.EventPress && ev.Keycode == vaxis.KeyEsc {
						return
					}
				case vaxis.FocusIn:
					focused = true
					s.Send(wire.Msg{Type: wire.MsgFocusGained})
					continue
				case vaxis.FocusOut:
					// Only report after a real focus: compositors can
					// emit a spurious FocusOut at map time, which would
					// otherwise close the menu as it opens.
					if focused {
						s.Send(wire.Msg{Type: wire.MsgFocusLost})
					}
					continue
				case vaxis.QuitEvent:
					return
				}
				s.events <- ev
			case m, ok := <-ctrl:
				if !ok {
					// Wire gone: the bar died or tore the stream down.
					return
				}
				if m.Type == wire.MsgClose {
					return
				}
				if m.Geo != nil {
					s.setGeometry(*m.Geo)
				}
				select {
				case s.msgs <- m:
				default:
				}
			}
		}
	}()

	return app(s)
}
