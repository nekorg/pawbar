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
)

// DDC/CI frame encoding and decoding, per VESA MCCS. Pure functions with no
// I/O: everything here is exercised by tests without a monitor attached.
//
// Only the direct /dev/i2c transport needs this — ddcutil-service speaks the
// protocol on our behalf.

const (
	// ddcAddr is the 7-bit I2C address every DDC/CI display listens on.
	ddcAddr = 0x37

	// hostSrc is the source byte we put in outgoing frames, and destSeed
	// the address byte their checksum is seeded with.
	hostSrc  = 0x51
	destSeed = 0x6E

	// replySeed is the seed for a reply's checksum. It is *not* destSeed
	// and *not* hostSrc: DDC/CI seeds the display's checksum with the
	// host's read address (0x50). Getting this wrong rejects every reply.
	replySeed = 0x50

	// VCPLuminance is the MCCS feature code for brightness.
	VCPLuminance = 0x10

	// getReplyLen is the exact size of a Get VCP Feature reply. We read
	// readLen instead: a bus with nothing left to clock reads back 0xFF,
	// which is harmless, while a display that prepends a pad byte would
	// otherwise desync us.
	getReplyLen = 11
	readLen     = 12
)

// DDC/CI timing (MCCS 2.2 §4.6). These are floors, not targets — going
// faster makes displays return null messages or garbage.
const (
	// getReplyDelay separates a request write from the reply read.
	getReplyDelay = 40 * time.Millisecond

	// postSetDelay separates a Set VCP from any following message. It is
	// what bounds how fast brightness can actually move.
	postSetDelay = 50 * time.Millisecond

	// nullRetryWait is how long to wait out a "busy" null message.
	nullRetryWait = 40 * time.Millisecond

	// nullRetries bounds how many times we wait out a busy display.
	nullRetries = 3
)

var (
	// ErrBusy is the display's null message: it is alive but not ready.
	// Retryable, and common enough that treating it as failure would make
	// perfectly good monitors look broken.
	ErrBusy = errors.New("ddc: display busy (null message)")

	// ErrChecksum is returned alongside otherwise valid values when the
	// reply's checksum does not match. Some displays compute it wrong;
	// the caller decides whether to retry or accept.
	ErrChecksum = errors.New("ddc: reply checksum mismatch")

	// ErrUnsupported means the display answered but does not implement the
	// requested feature.
	ErrUnsupported = errors.New("ddc: feature not supported by display")
)

// encodeGetVCP builds a Get VCP Feature request.
//
// For brightness this is 51 82 01 10 AC.
func encodeGetVCP(vcp byte) []byte {
	f := []byte{hostSrc, 0x82, 0x01, vcp, 0}
	f[4] = checksum(destSeed, f[:4])
	return f
}

// encodeSetVCP builds a Set VCP Feature request. There is no reply to a
// Set — which is exactly why the write path can be fast.
//
// For brightness 50 this is 51 84 03 10 00 32 9A.
func encodeSetVCP(vcp byte, v uint16) []byte {
	f := []byte{hostSrc, 0x84, 0x03, vcp, byte(v >> 8), byte(v), 0}
	f[6] = checksum(destSeed, f[:6])
	return f
}

// decodeGetReply parses a Get VCP Feature reply for vcp.
//
// A non-nil ErrChecksum is returned *with* usable cur/max: every structural
// check passed and only the checksum disagreed, so the caller can retry once
// and then choose to trust a display that simply computes it wrong. Every
// other error leaves cur/max meaningless.
func decodeGetReply(buf []byte, vcp byte) (cur, max uint16, err error) {
	off, err := replyOffset(buf)
	if err != nil {
		return 0, 0, err
	}
	f := buf[off:]

	// A null message (6E 80 BE) is a complete, valid frame meaning "busy".
	if f[1] == 0x80 {
		return 0, 0, ErrBusy
	}
	if n := f[1] & 0x7F; n != 8 {
		return 0, 0, fmt.Errorf("ddc: reply payload length %d, want 8", n)
	}
	if len(f) < getReplyLen {
		return 0, 0, fmt.Errorf("ddc: short reply: %d bytes, want %d", len(f), getReplyLen)
	}
	if f[2] != 0x02 {
		return 0, 0, fmt.Errorf("ddc: reply opcode 0x%02x, want 0x02", f[2])
	}
	if f[3] != 0x00 {
		return 0, 0, ErrUnsupported
	}
	if f[4] != vcp {
		return 0, 0, fmt.Errorf("ddc: reply is for feature 0x%02x, want 0x%02x", f[4], vcp)
	}

	max = uint16(f[6])<<8 | uint16(f[7])
	cur = uint16(f[8])<<8 | uint16(f[9])
	if max == 0 {
		return 0, 0, errors.New("ddc: display reports a maximum of 0")
	}
	if cur > max {
		cur = max
	}
	if got, want := f[10], checksum(replySeed, f[:10]); got != want {
		return cur, max, fmt.Errorf("%w: got 0x%02x, want 0x%02x", ErrChecksum, got, want)
	}
	return cur, max, nil
}

// replyOffset locates the frame header, tolerating a single leading pad byte.
func replyOffset(buf []byte) (int, error) {
	for _, off := range []int{0, 1} {
		if len(buf) >= off+3 && buf[off] == destSeed && buf[off+1]&0x80 != 0 {
			return off, nil
		}
	}
	if len(buf) < 3 {
		return 0, fmt.Errorf("ddc: reply too short: %d bytes", len(buf))
	}
	// All-0xFF is what an unpowered or absent display reads back.
	return 0, fmt.Errorf("ddc: no reply header (first bytes 0x%02x 0x%02x)", buf[0], buf[1])
}

// checksum is the running XOR DDC/CI uses, seeded with an address byte.
func checksum(seed byte, b []byte) byte {
	ck := seed
	for _, x := range b {
		ck ^= x
	}
	return ck
}

// pctFromRaw converts a display's raw value to a percentage. `max` is
// per-display: most report 100, but plenty do not.
func pctFromRaw(cur, max uint16) int {
	if max == 0 {
		return 0
	}
	if cur > max {
		cur = max
	}
	return (int(cur)*100 + int(max)/2) / int(max)
}

// rawFromPct is the inverse, clamping to the display's range.
func rawFromPct(pct int, max uint16) uint16 {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return uint16((pct*int(max) + 50) / 100)
}
