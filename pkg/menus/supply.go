// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/lease"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/internal/monitor"
)

// A bar does not pool menu panels. The supervisor does, for the whole
// desktop, and hands one over when this bar is clicked; see [Broker] and
// package lease. This file is the bar's end of that: where a menu panel
// comes from, and how it goes back.

// panelConn is a bar's grip on one menu panel: the wire, and the two halves
// of letting go of it.
type panelConn struct {
	enc *cbor.Encoder
	dec *cbor.Decoder

	// release hands the panel back to its owner, which closes and reaps it.
	release func()
	// free drops this process's mapping of the wire. Only safe once nothing
	// here reads it any more: the memory goes away under a blocked read.
	free func()
}

// panelSource supplies menu panels to this bar.
type panelSource interface {
	acquire() (*panelConn, error)
	pointer(on bool)
}

var (
	sourceMu sync.Mutex
	source   panelSource
)

// Connect attaches the bar to the supervisor's panel broker. Call it once
// the bar is up: menus then open without paying the kitty spawn cost, and
// the broker learns which monitor this bar is.
func Connect() { panels() }

// PointerPresent reports the pointer entering or leaving this bar. The
// spares live on the output the pointer is on — there is one cursor, so
// that is the only bar whose menus can be opened next.
func PointerPresent(on bool) { panels().pointer(on) }

func panels() panelSource {
	sourceMu.Lock()
	defer sourceMu.Unlock()
	if source == nil {
		source = dial()
	}
	return source
}

func dial() panelSource {
	c, err := lease.Dial(monitor.Self())
	if err != nil {
		logging.Log.Warn().Msgf("menus: no panel broker (%v); spawning menu panels locally", err)
		return local{}
	}
	return &leased{client: c}
}

// leased takes panels from the supervisor.
type leased struct {
	client *lease.Client
	on     atomic.Bool
}

func (s *leased) acquire() (*panelConn, error) {
	id, path, err := s.client.Acquire()
	if err != nil {
		return nil, err
	}
	// The wire is a katnip stream the supervisor created; taking it over is
	// seamless because its cursors live in the shared memory, and the host
	// says nothing between MsgReady and the MsgOpen below.
	stream, err := katnip.OpenStream(path)
	if err != nil {
		s.client.Release(id)
		return nil, fmt.Errorf("menus: attaching the panel wire: %w", err)
	}
	return newConn(stream, func() { s.client.Release(id) }, stream.Close), nil
}

func (s *leased) pointer(on bool) {
	if s.on.Swap(on) == on {
		return
	}
	if err := s.client.Pointer(on); err != nil {
		logging.Log.Debug().Msgf("menus: reporting the pointer: %v", err)
	}
}

// local is the fallback for a bar running without a supervisor (started by
// hand, or during development): it spawns its own panel per menu, cold. No
// pooling — one bar on its own has nobody to share spares with, and the
// pre-warming exists to be shared.
type local struct{}

func (local) acquire() (*panelConn, error) {
	mon, _ := monitor.Info()
	sp, err := warmSpare(mon, spawnHostPanel, nil)
	if err != nil {
		return nil, err
	}
	return newConn(sp.panel.ReadWriter(), sp.shutdown, sp.free), nil
}

func (local) pointer(bool) {}

func newConn(rw io.ReadWriter, release, free func()) *panelConn {
	var once sync.Once
	return &panelConn{
		enc:     cbor.NewEncoder(rw),
		dec:     cbor.NewDecoder(rw),
		release: release,
		// Unmapping twice could take out whatever mapping landed at that
		// address in between, so it happens exactly once.
		free: func() { once.Do(free) },
	}
}
