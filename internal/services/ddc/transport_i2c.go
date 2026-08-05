// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ddc

import (
	"errors"
	"fmt"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
	"golang.org/x/sys/unix"
)

// The direct /dev/i2c transport: DDC/CI spoken on the wire ourselves.
//
// The fd is opened once and held for the worker's lifetime. Nothing here
// reopens it, and no process is ever spawned.

// i2c-dev ioctls (linux/i2c-dev.h).
const (
	i2cSlave      = 0x0703
	i2cSlaveForce = 0x0706
)

// devI2C is a variable so tests need not have an i2c bus.
var devI2C = "/dev/i2c-"

type i2cTransport struct {
	fd  int
	bus int

	// lastOp is when the bus was last touched. DDC/CI needs a gap
	// between messages, and the display does not care that the gap it
	// needs happens to span two different calls.
	lastOp time.Time
}

func (t *i2cTransport) Name() string { return fmt.Sprintf("i2c-%d", t.bus) }

func openI2CTransport(d Display) (transport, error) {
	if d.I2CBus < 0 {
		return nil, errors.New("connector has no i2c bus")
	}
	path := fmt.Sprintf("%s%d", devI2C, d.I2CBus)

	// O_CLOEXEC is not optional: pawbar spawns processes for `run:`
	// actions and menus, and an inherited i2c fd is a real leak.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	if err := unix.IoctlSetInt(fd, i2cSlave, ddcAddr); err != nil {
		// EBUSY means a kernel driver (the out-of-tree ddcci module)
		// already claims 0x37. Forcing is safe here: we are speaking the
		// same protocol it would.
		if !errors.Is(err, unix.EBUSY) {
			unix.Close(fd)
			return nil, fmt.Errorf("%s: select DDC address: %w", path, err)
		}
		logging.Log.Debug().Msgf("ddc: %s: 0x%02x claimed by a kernel driver; forcing", path, ddcAddr)
		if err := unix.IoctlSetInt(fd, i2cSlaveForce, ddcAddr); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("%s: force DDC address: %w", path, err)
		}
	}

	t := &i2cTransport{fd: fd, bus: d.I2CBus}
	if _, _, err := t.Get(VCPLuminance); err != nil {
		t.Close()
		return nil, fmt.Errorf("%s: no DDC/CI display: %w", path, err)
	}
	return t, nil
}

// Get performs one Get VCP Feature transaction, waiting out a busy display.
func (t *i2cTransport) Get(vcp byte) (cur, max uint16, err error) {
	for attempt := range nullRetries {
		cur, max, err = t.get(vcp)
		switch {
		case err == nil:
			return cur, max, nil

		case errors.Is(err, ErrBusy):
			time.Sleep(nullRetryWait)

		case errors.Is(err, ErrChecksum):
			// Retry once; a display that answers structurally but
			// computes its checksum wrong is more common than one
			// returning convincing garbage, so on a second mismatch
			// take the value and say so once.
			if attempt > 0 {
				logging.Log.Warn().Msgf("ddc: i2c-%d: display checksums are wrong; trusting its values anyway", t.bus)
				return cur, max, nil
			}

		default:
			return 0, 0, err
		}
	}
	return 0, 0, err
}

func (t *i2cTransport) get(vcp byte) (cur, max uint16, err error) {
	t.settle()
	if err := t.write(encodeGetVCP(vcp)); err != nil {
		return 0, 0, err
	}

	// The display needs this long to have an answer ready; reading sooner
	// gets a null message at best.
	time.Sleep(getReplyDelay)

	buf := make([]byte, readLen)
	n, err := t.read(buf)
	if err != nil {
		return 0, 0, err
	}
	return decodeGetReply(buf[:n], vcp)
}

// Set performs a Set VCP Feature transaction. There is no reply to read.
func (t *i2cTransport) Set(vcp byte, v uint16) error {
	t.settle()
	return t.write(encodeSetVCP(vcp, v))
}

func (t *i2cTransport) Close() error {
	if t.fd < 0 {
		return nil
	}
	fd := t.fd
	t.fd = -1
	return unix.Close(fd)
}

// settle waits out whatever remains of the mandated gap since the last
// message. Pushing DDC/CI faster than this makes displays answer with null
// messages or nothing at all.
func (t *i2cTransport) settle() {
	if t.lastOp.IsZero() {
		return
	}
	if d := postSetDelay - time.Since(t.lastOp); d > 0 {
		time.Sleep(d)
	}
}

func (t *i2cTransport) write(b []byte) error {
	defer func() { t.lastOp = time.Now() }()
	for {
		_, err := unix.Write(t.fd, b)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("i2c write: %w", err)
		}
		return nil
	}
}

func (t *i2cTransport) read(b []byte) (int, error) {
	defer func() { t.lastOp = time.Now() }()
	for {
		n, err := unix.Read(t.fd, b)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("i2c read: %w", err)
		}
		return n, nil
	}
}
