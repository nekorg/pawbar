// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package module is pawbar's public module SDK.
//
// A module implements the Module interface plus any of the optional hook
// interfaces (MouseHandler, HoverObserver, StateObserver, Reconfigurer,
// Stopper). All hooks and source handlers run serially on one goroutine
// owned by the pawbar runtime: module code never needs channels, locks or
// select loops.
package module

import (
	"fmt"
	"strings"
	"time"

	"github.com/nekorg/pawbar/internal/lookup/colors"
	"github.com/nekorg/pawbar/internal/lookup/icons"
	"github.com/nekorg/pawbar/internal/lookup/units"
	"go.rockorager.dev/vaxis"
	"gopkg.in/yaml.v3"
)

// Color is a terminal color usable in yaml config (named, @var, #hex,
// rgb(r,g,b)).
type Color vaxis.Color

func (c *Color) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	col, err := colors.ParseColor(s)
	if err != nil {
		return err
	}
	*c = Color(col)
	return nil
}

func (c Color) Go() vaxis.Color { return vaxis.Color(c) }

func (c Color) MarshalYAML() (any, error) {
	p := c.Go().Params()
	switch len(p) {
	case 0:
		return "default", nil
	case 1:
		return fmt.Sprintf("index(%d)", p[0]), nil
	default:
		return fmt.Sprintf("#%02x%02x%02x", p[0], p[1], p[2]), nil
	}
}

// MustColor parses a color string (named, #hex, rgb()) and panics on
// failure. Intended for building Def defaults from constants.
func MustColor(s string) *Color {
	col, err := colors.ParseColor(s)
	if err != nil {
		panic(fmt.Sprintf("module.MustColor(%q): %v", s, err))
	}
	c := Color(col)
	return &c
}

// Cursor is a mouse pointer shape name (CSS cursor names).
type Cursor vaxis.MouseShape

func (c *Cursor) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	cur, err := ParseCursor(s)
	if err != nil {
		return err
	}
	*c = Cursor(cur)
	return nil
}

func (c Cursor) Go() vaxis.MouseShape { return vaxis.MouseShape(c) }

func (c Cursor) MarshalYAML() (any, error) { return string(c), nil }

// MustCursor parses a cursor name and panics on failure.
func MustCursor(s string) *Cursor {
	cur, err := ParseCursor(s)
	if err != nil {
		panic(fmt.Sprintf("module.MustCursor(%q): %v", s, err))
	}
	c := Cursor(cur)
	return &c
}

// ParseCursor maps a CSS cursor name to a vaxis mouse shape.
func ParseCursor(s string) (vaxis.MouseShape, error) {
	switch s {
	case "alias", "cell", "copy", "crosshair", "grab", "grabbing", "help",
		"move", "pointer", "progress", "text", "wait":
		return vaxis.MouseShape(s), nil
	case "default", "":
		return "default", nil
	case "e-resize":
		return "e", nil
	case "ew-resize":
		return "ew", nil
	case "n-resize":
		return "n", nil
	case "ne-resize":
		return "ne", nil
	case "nesw-resize":
		return "nesw", nil
	case "no-drop":
		return "no", nil
	case "not-allowed":
		return "not", nil
	case "ns-resize":
		return "ns", nil
	case "nw-resize":
		return "nw", nil
	case "nwse-resize":
		return "nwse", nil
	case "s-resize":
		return "s", nil
	case "se-resize":
		return "se", nil
	case "sw-resize":
		return "sw", nil
	case "vertical-text":
		return "vertical", nil
	case "w-resize":
		return "w", nil
	case "zoom-in", "zoom-out":
		return "zoom", nil
	default:
		return "", fmt.Errorf("invalid cursor name: %q", s)
	}
}

// Duration wraps time.Duration for yaml ("5s", "1m30s").
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	td, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(td)
	return nil
}

func (d Duration) Go() time.Duration { return time.Duration(d) }

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Percent is an integer bounded to 0-100.
type Percent int

func (p *Percent) UnmarshalYAML(n *yaml.Node) error {
	var s int
	if err := n.Decode(&s); err != nil {
		return err
	}
	if s < 0 || s > 100 {
		return fmt.Errorf("percentage should be between 0-100")
	}
	*p = Percent(s)
	return nil
}

func (p Percent) Go() int { return int(p) }

// Icon resolves an icon alias through pawbar's icon lookup table.
type Icon string

func (i *Icon) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	*i = Icon(icons.Resolve(raw))
	return nil
}

func (i Icon) Go() string { return string(i) }

// Scale selects a fixed byte unit or dynamic scaling.
type Scale struct {
	Dynamic bool
	Unit    units.Unit
}

func (s *Scale) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "", "auto", "dynamic", "adaptive":
		s.Dynamic = true
		return nil
	default:
		u, err := units.ParseUnit(raw)
		if err != nil {
			return err
		}
		s.Dynamic = false
		s.Unit = u
		return nil
	}
}

func (s Scale) MarshalYAML() (any, error) {
	if s.Dynamic {
		return "auto", nil
	}
	return s.Unit.Name, nil
}

// Direction is an up/down toggle.
type Direction bool

func (d *Direction) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	switch strings.ToLower(raw) {
	case "u", "up", "upward", "upwards":
		*d = true
	case "", "d", "down", "downward", "downwards":
		*d = false
	default:
		return fmt.Errorf("%q is not a valid direction. valid options are [%q, %q]", raw, "up", "down")
	}
	return nil
}

func (d Direction) IsUp() bool { return bool(d) }
func (d Direction) Go() bool   { return bool(d) }

// Ptr returns a pointer to v; convenience for building Block defaults
// (module.Ptr(true), module.Ptr(Duration(5*time.Second))).
func Ptr[T any](v T) *T { return &v }
