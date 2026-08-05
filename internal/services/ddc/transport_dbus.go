// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ddc

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/godbus/dbus/v5"
)

// The ddcutil-service transport.
//
// ddcutil-service is D-Bus activated, so the first call starts it and every
// call after that reaches a resident daemon — no process spawn per
// brightness change, which is the whole point.

const (
	ddcServiceName  = "com.ddcutil.DdcutilService"
	ddcServicePath  = "/com/ddcutil/DdcutilObject"
	ddcServiceIface = "com.ddcutil.DdcutilInterface"
)

// Service flag bits.
const (
	// flagEDIDPrefix matches edid_txt as a prefix rather than in full,
	// which is what lets us pass sysfs's EDID without caring that
	// ddcutil holds a different length of it.
	flagEDIDPrefix = 1

	// flagNoVerify disables libddcutil's read-back-and-retry after a set.
	//
	// Verification is the service's default and it doubles the cost of
	// every write. On an interactive scroll that is exactly the latency
	// this package exists to avoid, so the write path always sets it.
	flagNoVerify = 4
)

// edidPrefixLen is how much of the EDID we send. 126 = 42*3, so the base64
// encoding carries no padding and is therefore a genuine string prefix of
// the encoding of any longer blob — which is what flagEDIDPrefix compares.
const edidPrefixLen = 126

// displayByEDID tells the service to identify the display by EDID rather
// than by number. Display numbers get reallocated whenever the set of
// connected monitors changes; an EDID does not.
const displayByEDID = int32(-1)

type dbusTransport struct {
	conn *dbus.Conn
	obj  dbus.BusObject
	edid string
}

// ServiceAvailable reports whether ddcutil-service can be reached.
//
// Answered from the activatable-name list, so asking does not start the
// service, and cached because the answer cannot change without the user
// installing a package.
var ServiceAvailable = sync.OnceValue(func() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()

	var names []string
	call := conn.BusObject().Call("org.freedesktop.DBus.ListActivatableNames", 0)
	if call.Err != nil || call.Store(&names) != nil {
		return false
	}
	return slices.Contains(names, ddcServiceName)
})

func (t *dbusTransport) Name() string { return "ddcutil-service" }

func openDBusTransport(d Display) (transport, error) {
	edid := edidPrefix(d.EDID)
	if edid == "" {
		return nil, errors.New("no EDID to identify the display with")
	}

	// Private connection: Close is ours to call, and closing the shared
	// dbus.SessionBus() would break every other consumer in the process.
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}

	t := &dbusTransport{conn: conn, obj: conn.Object(ddcServiceName, ddcServicePath), edid: edid}

	if !ServiceAvailable() {
		conn.Close()
		return nil, fmt.Errorf("%s is not installed", ddcServiceName)
	}
	// The service being present does not mean it can see this display —
	// it may not have i2c access, or the monitor may not answer. One read
	// settles it before we commit.
	if _, _, err := t.Get(VCPLuminance); err != nil {
		conn.Close()
		return nil, fmt.Errorf("display not usable via ddcutil-service: %w", err)
	}
	return t, nil
}

// Get calls GetVcp(i display_number, s edid_txt, y vcp_code, u flags).
func (t *dbusTransport) Get(vcp byte) (cur, max uint16, err error) {
	var (
		formatted string
		status    int32
		msg       string
	)
	call := t.obj.Call(ddcServiceIface+".GetVcp", 0,
		displayByEDID, t.edid, vcp, uint32(flagEDIDPrefix))
	if call.Err != nil {
		return 0, 0, call.Err
	}
	if err := call.Store(&cur, &max, &formatted, &status, &msg); err != nil {
		return 0, 0, fmt.Errorf("GetVcp reply: %w", err)
	}
	if status != 0 {
		return 0, 0, serviceError("GetVcp", status, msg)
	}
	if max == 0 {
		return 0, 0, errors.New("ddc: display reports a maximum of 0")
	}
	if cur > max {
		cur = max
	}
	return cur, max, nil
}

// Set calls SetVcp(i display_number, s edid_txt, y vcp_code, q new_value,
// u flags), always with flagNoVerify — see its comment.
func (t *dbusTransport) Set(vcp byte, v uint16) error {
	var (
		status int32
		msg    string
	)
	call := t.obj.Call(ddcServiceIface+".SetVcp", 0,
		displayByEDID, t.edid, vcp, v, uint32(flagEDIDPrefix|flagNoVerify))
	if call.Err != nil {
		return call.Err
	}
	if err := call.Store(&status, &msg); err != nil {
		return fmt.Errorf("SetVcp reply: %w", err)
	}
	if status != 0 {
		return serviceError("SetVcp", status, msg)
	}
	return nil
}

func (t *dbusTransport) Close() error { return t.conn.Close() }

func serviceError(method string, status int32, msg string) error {
	if msg == "" {
		msg = "no detail"
	}
	return fmt.Errorf("ddcutil-service %s: status %d: %s", method, status, msg)
}

// edidPrefix base64-encodes the leading whole 3-byte groups of the EDID, so
// the result is padding-free and prefix-comparable.
func edidPrefix(edid []byte) string {
	n := min(len(edid), edidPrefixLen)
	n -= n % 3
	if n == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(edid[:n])
}
