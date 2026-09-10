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
// themselves: a panel is pinned to one output for its whole life, so the pool
// has to be spread across the monitors by intent, which only something that
// sees every bar can do.
type panelBroker struct {
	log      zerolog.Logger
	listener net.Listener
	broker   *menus.Broker
}

// startBroker begins serving panels. A supervisor that cannot listen keeps
// running: bars fall back to spawning their own panels, which is slower but
// not broken.
//
// shared says panels live in one kitty instance, which the first bar has yet
// to start. The pool waits for it rather than spawning a process per spare,
// which is the thing the instance exists to avoid.
func startBroker(log zerolog.Logger, shared bool, pool menus.PoolSettings) *panelBroker {
	l, err := lease.Listen()
	if err != nil {
		log.Warn().Msgf("menus: cannot serve menu panels (%v); each bar will spawn its own", err)
		return nil
	}
	b := &panelBroker{log: log, listener: l, broker: menus.NewBroker(pool)}
	logging.Go("supervisor.broker", b.serve)
	if shared {
		b.broker.Rehost(nil)
	}
	return b
}

// rehost points the pool at a new shared instance, or at none: a nil source
// suspends it until there is an instance to put a panel in again.
func (b *panelBroker) rehost(spawn func(outputs.Monitor) (*katnip.Panel, error)) {
	if b == nil {
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

// setOutputs tells the pool which monitors have a bar, which is what it
// spreads the spares over. Spares anywhere else are reclaimed: the monitor is
// gone, or nobody can click the bar that would open them.
func (b *panelBroker) setOutputs(names []string) {
	if b != nil {
		b.broker.SetOutputs(names)
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
