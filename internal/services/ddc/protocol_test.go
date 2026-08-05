// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package ddc

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeGetVCP(t *testing.T) {
	want := []byte{0x51, 0x82, 0x01, 0x10, 0xAC}
	if got := encodeGetVCP(VCPLuminance); !bytes.Equal(got, want) {
		t.Errorf("encodeGetVCP(0x10) = % 02x, want % 02x", got, want)
	}
}

func TestEncodeSetVCP(t *testing.T) {
	want := []byte{0x51, 0x84, 0x03, 0x10, 0x00, 0x32, 0x9A}
	if got := encodeSetVCP(VCPLuminance, 50); !bytes.Equal(got, want) {
		t.Errorf("encodeSetVCP(0x10, 50) = % 02x, want % 02x", got, want)
	}
}

// reply builds a well-formed Get VCP reply with a correct checksum.
func reply(vcp byte, cur, max uint16) []byte {
	f := []byte{
		0x6E, 0x88, 0x02, 0x00, vcp, 0x00,
		byte(max >> 8), byte(max),
		byte(cur >> 8), byte(cur),
		0,
	}
	f[10] = checksum(replySeed, f[:10])
	return f
}

func TestDecodeGetReply(t *testing.T) {
	cur, max, err := decodeGetReply(reply(VCPLuminance, 40, 100), VCPLuminance)
	if err != nil {
		t.Fatalf("decodeGetReply: %v", err)
	}
	if cur != 40 || max != 100 {
		t.Errorf("got cur=%d max=%d, want 40/100", cur, max)
	}
}

// The reply checksum is seeded with 0x50, not 0x6E or 0x51. Seeding it wrong
// rejects every reply from every monitor, so pin the exact bytes.
func TestDecodeGetReplyChecksumSeed(t *testing.T) {
	// Only 0x50 may be accepted. The two plausible wrong answers are the
	// display's write address and the host's own source byte.
	for _, tc := range []struct {
		name string
		seed byte
		ok   bool
	}{
		{"host read address 0x50", replySeed, true},
		{"display write address 0x6E", destSeed, false},
		{"host source byte 0x51", hostSrc, false},
	} {
		f := reply(VCPLuminance, 40, 100)
		f[10] = checksum(tc.seed, f[:10])

		_, _, err := decodeGetReply(f, VCPLuminance)
		if got := err == nil; got != tc.ok {
			t.Errorf("seeded with %s: accepted = %v, want %v (err=%v)", tc.name, got, tc.ok, err)
		}
	}
}

func TestDecodeGetReplyRejects(t *testing.T) {
	mangle := func(i int, v byte) []byte {
		f := reply(VCPLuminance, 40, 100)
		f[i] = v
		return f
	}
	for _, tc := range []struct {
		name string
		buf  []byte
	}{
		{"wrong opcode", mangle(2, 0x03)},
		{"wrong feature echo", mangle(4, 0x12)},
		{"payload length not 8", mangle(1, 0x86)},
		{"max of zero", func() []byte { f := reply(VCPLuminance, 0, 0); return f }()},
		{"no header", []byte{0x00, 0x00, 0x00, 0x00}},
		{"all ones", bytes.Repeat([]byte{0xFF}, readLen)},
		{"too short", []byte{0x6E}},
	} {
		if _, _, err := decodeGetReply(tc.buf, VCPLuminance); err == nil {
			t.Errorf("%s: accepted, want an error", tc.name)
		}
	}
}

// result != 0 means the display understood us and does not have the feature.
func TestDecodeGetReplyUnsupported(t *testing.T) {
	f := reply(VCPLuminance, 40, 100)
	f[3] = 0x01
	f[10] = checksum(replySeed, f[:10])

	if _, _, err := decodeGetReply(f, VCPLuminance); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// A bad checksum must still surface the values, so the caller can retry once
// and then choose to trust a checksum-buggy display.
func TestDecodeGetReplyChecksumMismatchKeepsValues(t *testing.T) {
	f := reply(VCPLuminance, 40, 100)
	f[10] ^= 0xFF

	cur, max, err := decodeGetReply(f, VCPLuminance)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if cur != 40 || max != 100 {
		t.Errorf("got cur=%d max=%d, want the values preserved (40/100)", cur, max)
	}
}

// The null message is "busy", not "broken" — you hit it on real hardware.
func TestDecodeGetReplyNullMessage(t *testing.T) {
	for _, buf := range [][]byte{
		{0x6E, 0x80, 0xBE},
		append([]byte{0x6E, 0x80, 0xBE}, bytes.Repeat([]byte{0xFF}, 9)...),
	} {
		if _, _, err := decodeGetReply(buf, VCPLuminance); !errors.Is(err, ErrBusy) {
			t.Errorf("decodeGetReply(% 02x) err = %v, want ErrBusy", buf, err)
		}
	}
}

// We read 12 bytes for an 11-byte frame, and some displays prepend a pad.
func TestDecodeGetReplyFraming(t *testing.T) {
	t.Run("trailing 0xFF", func(t *testing.T) {
		buf := append(reply(VCPLuminance, 40, 100), 0xFF)
		cur, max, err := decodeGetReply(buf, VCPLuminance)
		if err != nil || cur != 40 || max != 100 {
			t.Errorf("got cur=%d max=%d err=%v, want 40/100/nil", cur, max, err)
		}
	})
	t.Run("leading pad byte", func(t *testing.T) {
		buf := append([]byte{0x00}, reply(VCPLuminance, 40, 100)...)
		cur, max, err := decodeGetReply(buf, VCPLuminance)
		if err != nil || cur != 40 || max != 100 {
			t.Errorf("got cur=%d max=%d err=%v, want 40/100/nil", cur, max, err)
		}
	})
}

// A display with max=64 has 65 settable levels, so 101 percentages cannot
// survive a round trip exactly — that is quantization, not error. What must
// hold is that the endpoints are exact, the mapping never overshoots the
// display's range or reverses, and the drift stays inside one step.
func TestPctRawRoundTrip(t *testing.T) {
	for _, max := range []uint16{100, 255, 64, 10} {
		tol := 100/(2*int(max)) + 1

		prev := -1
		for pct := 0; pct <= 100; pct++ {
			raw := rawFromPct(pct, max)
			if raw > max {
				t.Fatalf("max=%d pct=%d: raw %d exceeds max", max, pct, raw)
			}
			if int(raw) < prev {
				t.Errorf("max=%d pct=%d: raw %d went backwards from %d", max, pct, raw, prev)
			}
			prev = int(raw)

			if got := pctFromRaw(raw, max); got < pct-tol || got > pct+tol {
				t.Errorf("max=%d: pct %d -> raw %d -> pct %d, drift exceeds %d",
					max, pct, raw, got, tol)
			}
		}

		if got := rawFromPct(0, max); got != 0 {
			t.Errorf("max=%d: rawFromPct(0) = %d, want 0", max, got)
		}
		if got := rawFromPct(100, max); got != max {
			t.Errorf("max=%d: rawFromPct(100) = %d, want %d", max, got, max)
		}
		if got := pctFromRaw(0, max); got != 0 {
			t.Errorf("max=%d: pctFromRaw(0) = %d, want 0", max, got)
		}
		if got := pctFromRaw(max, max); got != 100 {
			t.Errorf("max=%d: pctFromRaw(max) = %d, want 100", max, got)
		}
	}
}

func TestPctRawClamps(t *testing.T) {
	if got := rawFromPct(150, 100); got != 100 {
		t.Errorf("rawFromPct(150,100) = %d, want 100", got)
	}
	if got := rawFromPct(-5, 100); got != 0 {
		t.Errorf("rawFromPct(-5,100) = %d, want 0", got)
	}
	if got := pctFromRaw(200, 100); got != 100 {
		t.Errorf("pctFromRaw(200,100) = %d, want 100", got)
	}
	if got := pctFromRaw(5, 0); got != 0 {
		t.Errorf("pctFromRaw(5,0) = %d, want 0", got)
	}
}
