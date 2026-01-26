// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package modules

import (
	"git.sr.ht/~rockorager/vaxis"
)

var (
	BLACK   = vaxis.IndexColor(0)
	URGENT  = vaxis.IndexColor(9)
	WARNING = vaxis.IndexColor(11)
	GOOD    = vaxis.IndexColor(2)
	ACTIVE  = vaxis.IndexColor(15)
	COOL    = vaxis.RGBColor(173, 216, 230)
	SPECIAL = vaxis.RGBColor(0, 100, 0)
)

func Cell(r rune, s vaxis.Style) vaxis.Cell {
	return vaxis.Cell{Character: vaxis.Characters(string(r))[0], Style: s}
}

type EventCell struct {
	C          vaxis.Cell
	Metadata   string
	Mod        Module
	MouseShape vaxis.MouseShape
}

type Module interface {
	Render() []EventCell
	Run() (<-chan bool, chan<- Event, error)
	Channels() (<-chan bool, chan<- Event)
	Name() string
	Dependencies() []string
}

type Event struct {
	Cell       EventCell
	VaxisEvent vaxis.Event
}

type FocusIn struct {
	NewMod  Module
	PrevMod Module
}
type FocusOut struct {
	NewMod  Module
	PrevMod Module
}

type SystemWake struct {
	Source string
}

func (FocusIn) String() string    { return "FocusIn" }
func (FocusOut) String() string   { return "FocusOut" }
func (SystemWake) String() string { return "SystemWake" }

var (
	ECSPACE = EventCell{
		C:          vaxis.Cell{Character: vaxis.Characters(" ")[0]},
		Metadata:   "",
		Mod:        nil,
		MouseShape: "",
	}
	ECDOT = EventCell{
		C:          vaxis.Cell{Character: vaxis.Characters(".")[0]},
		Metadata:   "",
		Mod:        nil,
		MouseShape: "",
	}
	ECELLIPSIS = EventCell{
		C:          vaxis.Cell{Character: vaxis.Characters("…")[0]},
		Metadata:   "",
		Mod:        nil,
		MouseShape: "",
	}
)
