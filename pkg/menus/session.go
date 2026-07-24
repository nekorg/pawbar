// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	l "log"
	"os"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
	"go.rockorager.dev/vaxis"
)

// hostInstance is the single katnip re-exec identity every menu panel runs
// as. The concrete renderer is chosen per-open from the MsgOpen Kind, not
// from the process identity, so one warm panel can serve any menu.
const hostInstance = "pawpanel"

const (
	// mapWait bounds the wait for the off-screen map to report its size.
	mapWait = 2 * time.Second
	// resizeWait bounds the wait for a resize to a target size to land.
	resizeWait = 2 * time.Second
)

// AppFunc is the child-side entry point of a menu: it runs inside the
// menu's kitty process with the boilerplate (vaxis, wire decoding,
// Esc/close handling, focus reporting) already wired up. It should
// consume s.Events() until the channel closes, then return.
type AppFunc func(s *Session) int

// appRegistry maps a menu Kind to its renderer. Populated by Register from
// package init()s; read by the host after it receives MsgOpen (well after
// all init()s, so cross-package registration order does not matter).
var appRegistry = map[string]AppFunc{}

// Register wires a menu renderer under a Kind. Call it from an init() of a
// package linked into the binary. The Kind travels in MsgOpen; it is no
// longer a katnip identity.
func Register(name string, app AppFunc) {
	appRegistry[name] = app
}

func appLookup(kind string) AppFunc { return appRegistry[kind] }

// RegisterHost wires the single generic menu-host identity into katnip's
// re-exec dispatch. Call it once, from the top-level command's init AFTER
// every Register call, so appRegistry is fully populated before a
// re-exec'd host dispatches on Kind (a matching katnip identity runs its
// handler during init and never returns, halting later init()s).
func RegisterHost() {
	katnip.RegisterFunc(hostInstance, func(k *katnip.Kitty, rw io.ReadWriter) int {
		logging.SetupFileOnly(hostInstance)
		return runHost(k, rw)
	})
}

// Session is a menu app's handle to its panel: the vaxis screen, the
// filtered event stream and the wire channel back to the bar.
type Session struct {
	vx    *vaxis.Vaxis
	k     *katnip.Kitty
	fg    color.RGBA
	enc   *cbor.Encoder
	encMu sync.Mutex

	events chan vaxis.Event
	msgs   chan wire.Msg

	// revealed guards the one-time on-screen reveal (Move) on first paint.
	// Touched only by the app goroutine (the sole caller of Render).
	revealed bool

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

// Foreground is the terminal's default foreground color, queried once
// during warm-up so apps don't pay the OSC round-trip on the open path.
func (s *Session) Foreground() color.RGBA { return s.fg }

// Render flushes the current frame. The first call also reveals the
// panel: it was rendered off-screen at its target size, so a single
// panel-reconfigure slides it on-screen already sized and painted (no
// blank flash) and grants it keyboard focus.
func (s *Session) Render() {
	s.vx.Render()
	if !s.revealed {
		s.revealed = true
		// Reveal at the bar's clamped position. The bar computed it with
		// correct on-screen cell metrics; the spare measured itself while
		// parked off-screen (a different/absent output scale), so its own
		// metrics must not re-clamp the placement here. The true on-screen
		// metrics are recorded by hostLoop from the resize this move
		// triggers, in time for submenu placement.
		geo := s.Geometry()
		s.reveal(geo.PanelX, geo.PanelY)
	}
}

// reveal slides the panel on-screen to (x,y) and switches it to exclusive
// keyboard focus in one panel-reconfigure. Spares idle off-screen with a
// non-grabbing focus policy (so they never steal the user's keyboard);
// revealing grants focus, so a warmed menu behaves exactly like one
// spawned exclusive. Uses kitty's resize-os-window os-panel action
// directly (katnip exposes no focus-policy helper yet).
func (s *Session) reveal(x, y int) {
	err := s.k.Dispatch("resize-os-window", map[string]any{
		"action":      "os-panel",
		"incremental": true,
		"os_panel": []string{
			fmt.Sprintf("margin-left=%d", x),
			fmt.Sprintf("margin-top=%d", y),
			"focus-policy=exclusive",
		},
	})
	if err != nil {
		logging.Log.Warn().Msgf("menus: reveal failed: %v", err)
		s.k.Move(x, y) // fall back to at least moving on-screen
	}
}

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

// reclampPos computes an on-screen-clamped position for a cols x rows
// panel using this panel's own measured cell metrics — the ground truth
// for the surface actually on screen, which the bar's initial estimate
// (made with the bar's font, not the panel's pinned one) can miss. It
// reports whether the result differs from the current placement.
func (s *Session) reclampPos(cols, rows int) (int, int, wire.Geometry, bool) {
	geo := s.Geometry()
	if geo.MonW <= 0 || geo.Scale <= 0 {
		return geo.PanelX, geo.PanelY, geo, false
	}
	ppcX, ppcY := geo.PPCX, geo.PPCY
	if mx, my := s.MeasuredPPC(); mx > 0 && my > 0 {
		ppcX, ppcY = mx, my
	}
	w := cellsToLogical(cols, ppcX, geo.Scale) + 2*geo.Pad
	h := cellsToLogical(rows, ppcY, geo.Scale) + 2*geo.Pad
	x := clamp(geo.PanelX, 0, geo.MonW-w)
	y := clamp(geo.PanelY, 0, geo.MonH-h)
	return x, y, geo, x != geo.PanelX || y != geo.PanelY
}

func (s *Session) applyMove(x, y int, geo wire.Geometry, cols, rows int) {
	s.k.Move(x, y)
	geo.PanelX, geo.PanelY = x, y
	s.setGeometry(geo)
	s.Send(wire.Msg{Type: wire.MsgResized, Cols: cols, Rows: rows, Geo: &geo})
}

// Resize grows or shrinks the panel to cols x rows, moving it first if
// the new size would clip off the monitor. It reports the resulting
// placement back to the bar so submenu math stays accurate.
func (s *Session) Resize(cols, rows int) {
	x, y, geo, moved := s.reclampPos(cols, rows)
	if moved {
		s.k.Move(x, y)
		geo.PanelX, geo.PanelY = x, y
		s.setGeometry(geo)
	}
	s.k.Resize(cols, rows)
	s.Send(wire.Msg{Type: wire.MsgResized, Cols: cols, Rows: rows, Geo: &geo})
}

// Reposition re-clamps the panel to the monitor without resizing it. The
// root menu is spawned at its final cell size, so Resize never fires to
// correct an initial placement made with the bar's (possibly mismatched)
// cell metrics; the child calls this once it knows its true size.
func (s *Session) Reposition(cols, rows int) {
	if x, y, geo, moved := s.reclampPos(cols, rows); moved {
		s.applyMove(x, y, geo, cols, rows)
	}
}

// runHost is the generic menu host: one long-lived process that warms up
// (mapped off-screen), waits to be assigned a menu, sizes itself to the
// target, then runs the chosen renderer. The expensive kitty spawn,
// vaxis handshake and foreground query are all paid before MsgOpen.
func runHost(k *katnip.Kitty, rw io.ReadWriter) int {
	l.SetOutput(io.Discard)

	vx, err := vaxis.New(vaxis.Options{
		WithTTY:         os.Stdout.Name(),
		EnableSGRPixels: true,
	})
	if err != nil {
		return 1
	}
	defer vx.Close()

	fg := queryForeground(vx)

	var enc *cbor.Encoder
	ctrl := make(chan wire.Msg, 16)
	if rw != nil {
		enc = cbor.NewEncoder(rw)
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

	// WARM: map off-screen so the map round-trip is paid before any menu
	// opens, then wait for the panel to SETTLE at its spawn size. A freshly
	// mapped panel first reports a transient default at the wrong output
	// scale and only then settles to its configured size at the real scale;
	// announcing readiness before the settle would let a menu render at the
	// wrong metrics (wrong column count -> wrapped rows on reveal).
	k.Show()
	if r, ok := awaitResize(vx, warmCols, warmRows, mapWait); ok {
		vx.Resize(r) // apply to vaxis so Size()/buffers track the real size
	}
	if enc != nil {
		_ = enc.Encode(wire.Msg{Type: wire.MsgReady})
	}

	// Idle until the bar assigns this spare a menu. Drain vaxis events so a
	// long-idle spare never wedges vaxis on a full event buffer.
	open, ok := waitOpen(vx, ctrl)
	if !ok {
		return 0
	}

	s := &Session{
		vx:     vx,
		k:      k,
		fg:     fg,
		enc:    enc,
		events: make(chan vaxis.Event, 32),
		msgs:   make(chan wire.Msg, 16),
	}
	if open.Geo != nil {
		s.geo = *open.Geo
	}
	// Seed the panel's measured cell metrics from the settled size. The
	// spare is pinned to its output, so it is already at the on-screen scale
	// even while parked; revealing it fires no resize event, so submenu row
	// alignment must take real pixels-per-cell from here, not the bar's
	// estimate.
	{
		ws := vx.Size()
		s.setSize(ws.Cols, ws.Rows, ws.XPixel, ws.YPixel)
	}

	// Size to the target while still off-screen (at the settled real scale),
	// so the app draws at the final size. Skip when already the right size
	// (a menu that happens to match the spawn size), since no resize event
	// would fire to wait for.
	if open.Cols > 0 && open.Rows > 0 {
		if c, r := vx.Window().Size(); c != open.Cols || r != open.Rows {
			k.Resize(open.Cols, open.Rows)
			if ev, ok := awaitResize(vx, open.Cols, open.Rows, resizeWait); ok {
				vx.Resize(ev) // update vaxis size before the app draws
				s.setSize(ev.Cols, ev.Rows, ev.XPixel, ev.YPixel)
			}
		}
	}
	// Discard any trailing resize events so the app's first draw is real
	// content (which triggers the on-screen reveal), not an empty frame.
	drainEvents(vx)

	app := appLookup(open.Kind)
	if app == nil {
		logging.Log.Error().Msgf("menus: unknown menu kind %q", open.Kind)
		return 1
	}

	go s.hostLoop(vx, ctrl)
	return app(s)
}

// hostLoop is the per-menu boilerplate: it is the only sender on s.events
// and the only party that closes it; the app exits by draining the closed
// channel. Esc and focus events are handled here and never reach the app.
func (s *Session) hostLoop(vx *vaxis.Vaxis, ctrl <-chan wire.Msg) {
	defer close(s.events)
	focused := false
	for {
		select {
		case ev := <-vx.Events():
			switch ev := ev.(type) {
			case vaxis.Resize:
				vx.Resize(ev) // keep vaxis size/buffers in sync before the app redraws
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
				// Only report after a real focus: compositors can emit a
				// spurious FocusOut at map time, which would otherwise
				// close the menu as it opens.
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
}

// awaitResize consumes vaxis events until a Resize (matching cols x rows,
// or any when cols == 0) arrives, or the timeout elapses. Used only in the
// single-consumer warm/resize phases, before hostLoop takes over vx.Events.
func awaitResize(vx *vaxis.Vaxis, cols, rows int, timeout time.Duration) (vaxis.Resize, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-vx.Events():
			if r, ok := ev.(vaxis.Resize); ok {
				if cols == 0 || (r.Cols == cols && r.Rows == rows) {
					return r, true
				}
			}
		case <-deadline:
			return vaxis.Resize{}, false
		}
	}
}

// waitOpen blocks until the bar sends MsgOpen, draining vaxis events in the
// meantime so an idle spare never stalls vaxis. Returns false if the wire
// closes or MsgClose arrives first.
func waitOpen(vx *vaxis.Vaxis, ctrl <-chan wire.Msg) (wire.Msg, bool) {
	for {
		select {
		case <-vx.Events():
			// discard while idle/off-screen
		case m, ok := <-ctrl:
			if !ok || m.Type == wire.MsgClose {
				return wire.Msg{}, false
			}
			if m.Type == wire.MsgOpen {
				return m, true
			}
		}
	}
}

func drainEvents(vx *vaxis.Vaxis) {
	for {
		select {
		case <-vx.Events():
		default:
			return
		}
	}
}

func queryForeground(vx *vaxis.Vaxis) color.RGBA {
	c := vx.QueryForeground()
	rgb := c.Params()
	return color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
}
