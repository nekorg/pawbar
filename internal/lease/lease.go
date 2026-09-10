// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package lease is the CBOR protocol a bar speaks to the supervisor to get
// a menu panel.
//
// Panels are pooled by the supervisor, not by the bars: kitty pins a panel
// to its output at creation, so a warm spare only ever serves the monitor it
// was warmed on, and there is only one mouse cursor to open menus with. Bars
// therefore report where the pointer is and ask for a panel when clicked;
// which output to keep spares on is the supervisor's call.
package lease

import (
	"net"
	"os"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/nekorg/pawbar/internal/session"
)

// SocketPath is where the supervisor listens.
func SocketPath() string { return session.RuntimePath("sock") }

type MsgType int

const (
	// bar -> supervisor
	MsgHello   MsgType = iota // which bar this connection is
	MsgPointer                // the pointer entered (On) or left this bar
	MsgAcquire                // give me a panel on my output
	MsgRelease                // done with lease ID, reclaim it (Warm: it parked itself)

	// supervisor -> bar
	MsgGranted // lease ID is yours; attach its wire at Path
	MsgDenied  // no panel: Err says why
)

type Msg struct {
	Type   MsgType
	ID     uint64 `cbor:",omitempty"` // lease id (MsgGranted, MsgRelease)
	Output string `cbor:",omitempty"` // this bar's monitor (MsgHello)
	Path   string `cbor:",omitempty"` // katnip stream to attach (MsgGranted)
	Err    string `cbor:",omitempty"` // MsgDenied
	On     bool   `cbor:",omitempty"` // MsgPointer
	Warm   bool   `cbor:",omitempty"` // MsgRelease: the panel parked itself and can be reused
}

// acquireTimeout bounds a bar's wait for a panel. A warm spare answers in
// microseconds; a miss costs a kitty spawn, and past that the supervisor is
// in trouble and the bar is better off failing the click than hanging.
const acquireTimeout = 6 * time.Second

// Client is a bar's connection to the supervisor. One request is in flight
// at a time: menus open one at a time anyway.
type Client struct {
	mu   sync.Mutex
	conn net.Conn
	enc  *cbor.Encoder
	dec  *cbor.Decoder
}

// Dial connects and announces which bar this is.
func Dial(output string) (*Client, error) {
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, enc: cbor.NewEncoder(conn), dec: cbor.NewDecoder(conn)}
	if err := c.send(Msg{Type: MsgHello, Output: output}); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) send(m Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(m)
}

// Pointer reports that the pointer entered or left this bar, which is how
// the supervisor decides where to keep the spares.
func (c *Client) Pointer(on bool) error { return c.send(Msg{Type: MsgPointer, On: on}) }

// Release hands a leased panel back. warm says the panel answered MsgClose by
// parking itself, so the supervisor can put it straight back in the pool;
// without it the panel is in an unknown state and gets killed.
func (c *Client) Release(id uint64, warm bool) error {
	return c.send(Msg{Type: MsgRelease, ID: id, Warm: warm})
}

// Acquire leases a panel for this bar's output and returns its id and the
// katnip stream path to attach to.
func (c *Client) Acquire() (uint64, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.enc.Encode(Msg{Type: MsgAcquire}); err != nil {
		return 0, "", err
	}
	c.conn.SetReadDeadline(time.Now().Add(acquireTimeout))
	defer c.conn.SetReadDeadline(time.Time{})

	var m Msg
	if err := c.dec.Decode(&m); err != nil {
		return 0, "", err
	}
	if m.Type != MsgGranted {
		return 0, "", &Error{m.Err}
	}
	return m.ID, m.Path, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Error is a refusal from the supervisor.
type Error struct{ Msg string }

func (e *Error) Error() string {
	if e.Msg == "" {
		return "no panel available"
	}
	return e.Msg
}

// Listen opens the supervisor's socket, replacing a stale one left by a
// pawbar that died. Safe because the caller holds the session lock: nothing
// else may be listening.
func Listen() (net.Listener, error) {
	path := SocketPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	os.Chmod(path, 0o600)
	return l, nil
}
