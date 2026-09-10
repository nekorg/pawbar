// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"errors"
	"io"
	"net"

	"github.com/codelif/outputs"
	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/lease"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus"
	"github.com/rs/zerolog"
)

// panelBroker serves the menu panels for every bar. Bars do not pool panels
// themselves: a panel is pinned to one output for its whole life, and with a
// single mouse cursor only the bar under it can be clicked next, so two warm
// spares cover the whole desktop instead of two per monitor.
type panelBroker struct {
	log      zerolog.Logger
	listener net.Listener
	broker   *menus.Broker
}

// startBroker begins serving panels. A supervisor that cannot listen keeps
// running: bars fall back to spawning their own panels, which is slower but
// not broken.
func startBroker(log zerolog.Logger, spawn func(outputs.Monitor) (*katnip.Panel, error)) *panelBroker {
	l, err := lease.Listen()
	if err != nil {
		log.Warn().Msgf("menus: cannot serve menu panels (%v); each bar will spawn its own", err)
		return nil
	}
	b := &panelBroker{log: log, listener: l, broker: menus.NewBroker()}
	logging.Go("supervisor.broker", b.serve)
	// Warm the first spares on the primary output, so even the first menu of
	// the session opens without paying the panel spawn cost.
	if spawn != nil {
		b.broker.Rehost(spawn)
	} else {
		b.broker.Warm()
	}
	return b
}

// rehost moves the pool to a new shared instance after the old one died.
func (b *panelBroker) rehost(spawn func(outputs.Monitor) (*katnip.Panel, error)) {
	if b == nil || spawn == nil {
		return
	}
	b.broker.Rehost(spawn)
}

func (b *panelBroker) serve() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return // the listener was closed
		}
		go b.session(conn)
	}
}

// session serves one bar until its connection drops, which is also how a
// dead bar's panels are reclaimed.
func (b *panelBroker) session(conn net.Conn) {
	var (
		output string
		held   []uint64
	)
	defer func() {
		conn.Close()
		if len(held) > 0 {
			b.log.Debug().Msgf("menus: %s went away holding %d panel(s), reclaiming", label(output), len(held))
			b.broker.ReleaseAll(held)
		}
	}()

	enc := cbor.NewEncoder(conn)
	dec := cbor.NewDecoder(conn)
	for {
		var m lease.Msg
		if err := dec.Decode(&m); err != nil {
			if !errors.Is(err, io.EOF) {
				b.log.Debug().Msgf("menus: %s connection: %v", label(output), err)
			}
			return
		}

		switch m.Type {
		case lease.MsgHello:
			output = m.Output

		case lease.MsgPointer:
			b.log.Debug().Msgf("menus: pointer %s %s", map[bool]string{true: "entered", false: "left"}[m.On], label(output))
			if m.On {
				b.broker.WarmFor(output)
			}

		case lease.MsgAcquire:
			id, path, err := b.broker.Acquire(output)
			if err != nil {
				b.log.Warn().Msgf("menus: no panel for %s: %v", label(output), err)
				enc.Encode(lease.Msg{Type: lease.MsgDenied, Err: err.Error()})
				continue
			}
			if err := enc.Encode(lease.Msg{Type: lease.MsgGranted, ID: id, Path: path}); err != nil {
				b.broker.Release(id)
				return
			}
			held = append(held, id)

		case lease.MsgRelease:
			b.broker.Release(m.ID)
			held = remove(held, m.ID)
		}
	}
}

// dropOutput reclaims the spares warmed for a monitor that is no longer
// there; they carry its old geometry and scale.
func (b *panelBroker) dropOutput(name string) {
	if b != nil {
		b.broker.Drop(name)
	}
}

func (b *panelBroker) stop() {
	if b == nil {
		return
	}
	b.listener.Close()
	b.broker.Close()
}

func remove(ids []uint64, id uint64) []uint64 {
	for i, held := range ids {
		if held == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}
