// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"errors"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
)

// poolTarget is how many warm spares to keep hot. One spare keeps the
// invariant "live panels = visible + 1": opening a menu (or submenu)
// consumes the spare and refills in the background.
const poolTarget = 2

// readyTimeout bounds waiting for a freshly spawned spare to warm up
// (kitty spawn + vaxis handshake + off-screen map). Generous; the wait
// returns as soon as MsgReady arrives.
const readyTimeout = 5 * time.Second

// warmPanel is a spawned, mapped-off-screen host that has announced
// MsgReady and is waiting for MsgOpen. Its enc/dec are handed to the
// per-menu Handle on acquire, so exactly one decoder ever reads the wire.
type warmPanel struct {
	panel *katnip.Panel
	enc   *cbor.Encoder
	dec   *cbor.Decoder
}

type panelPool struct {
	mu      sync.Mutex
	idle    []*warmPanel
	warming int
}

var pool panelPool

// PrewarmPool starts warming the first spare. Call once the bar is up so
// even the first menu opens without paying the kitty spawn cost.
func PrewarmPool() { pool.ensureWarm() }

// acquire returns a warm spare, cold-spawning one synchronously when the
// pool is empty (the same warm path, just not pre-paid), then tops the
// pool back up in the background.
func (p *panelPool) acquire() (*warmPanel, error) {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		wp := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		p.ensureWarm()
		return wp, nil
	}
	p.mu.Unlock()

	logging.Log.Debug().Msg("menus: pool empty, cold-spawning a spare")
	wp, err := spawnWarm()
	if err != nil {
		return nil, err
	}
	if !wp.awaitReady() {
		_ = wp.panel.Kill()
		return nil, errors.New("menus: spare died before ready")
	}
	p.ensureWarm()
	return wp, nil
}

// ensureWarm tops the idle pool up to poolTarget, spawning in the
// background so it never blocks an open.
func (p *panelPool) ensureWarm() {
	p.mu.Lock()
	need := poolTarget - len(p.idle) - p.warming
	if need < 0 {
		need = 0
	}
	p.warming += need
	p.mu.Unlock()
	for i := 0; i < need; i++ {
		go p.warmOne()
	}
}

func (p *panelPool) warmOne() {
	wp, err := spawnWarm()
	if err == nil && !wp.awaitReady() {
		_ = wp.panel.Kill()
		wp, err = nil, errors.New("spare died before ready")
	}
	p.mu.Lock()
	p.warming--
	if err == nil {
		p.idle = append(p.idle, wp)
	}
	p.mu.Unlock()
	if err != nil {
		logging.Log.Warn().Msgf("menus: warming spare: %v", err)
	}
}

func spawnWarm() (*warmPanel, error) {
	panel, err := spawnHostPanel()
	if err != nil {
		return nil, err
	}
	return &warmPanel{
		panel: panel,
		enc:   cbor.NewEncoder(panel.Writer()),
		dec:   cbor.NewDecoder(panel.Reader()),
	}, nil
}

// awaitReady blocks until the spare sends MsgReady, or the wire closes /
// the timeout elapses. The decoder is left positioned right after
// MsgReady; the host stays silent until it receives MsgOpen, so the Handle
// reuses this same decoder without losing a byte.
func (wp *warmPanel) awaitReady() bool {
	res := make(chan bool, 1)
	go func() {
		for {
			var m wire.Msg
			if err := wp.dec.Decode(&m); err != nil {
				res <- false
				return
			}
			if m.Type == wire.MsgReady {
				res <- true
				return
			}
		}
	}()
	select {
	case ok := <-res:
		return ok
	case <-time.After(readyTimeout):
		return false
	}
}
