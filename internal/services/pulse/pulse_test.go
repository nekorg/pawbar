// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package pulse

import (
	"errors"
	"testing"
	"time"

	"github.com/codelif/pulseaudio"
)

func TestInteresting(t *testing.T) {
	for _, op := range []uint32{pulseaudio.EvNew, pulseaudio.EvChange, pulseaudio.EvRemove} {
		for _, f := range []uint32{pulseaudio.EvSink, pulseaudio.EvServer} {
			if !interesting(pulseaudio.Event{Facility: f, Op: op}) {
				t.Errorf("facility %#x op %#x should be interesting", f, op)
			}
		}
	}
	// a stream's own volume says nothing about the default sink.
	for _, f := range []uint32{pulseaudio.EvSinkInput, pulseaudio.EvSource, pulseaudio.EvClient} {
		if interesting(pulseaudio.Event{Facility: f, Op: pulseaudio.EvChange}) {
			t.Errorf("facility %#x should be ignored", f)
		}
	}
}

func TestVolumePct(t *testing.T) {
	for _, c := range []struct {
		in   pulseaudio.Cvolume
		want float64
	}{
		{nil, 0},
		{pulseaudio.Cvolume{0}, 0},
		{pulseaudio.Cvolume{0xffff, 0xffff}, 100},
		{pulseaudio.Cvolume{0x7fff}, 49.999237048905165},
	} {
		if got := volumePct(c.in); got != c.want {
			t.Errorf("volumePct(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDefaultSinkAndLabel(t *testing.T) {
	st := State{Connected: true, Default: "b", Sinks: []SinkInfo{
		{Name: "a", Description: "Speakers"},
		{Name: "b", Volume: 40},
	}}
	s, ok := st.DefaultSink()
	if !ok || s.Name != "b" || s.Volume != 40 {
		t.Fatalf("DefaultSink() = %+v, %v", s, ok)
	}
	if got := s.Label(); got != "b" {
		t.Errorf("Label() with no description = %q, want the sink name", got)
	}
	if got := st.Sinks[0].Label(); got != "Speakers" {
		t.Errorf("Label() = %q, want the description", got)
	}

	if _, ok := (State{Default: "gone"}).DefaultSink(); ok {
		t.Error("DefaultSink() found a sink that is not in the list")
	}
}

// A wedged server must never reach the caller. send drops commands
// instead of blocking; that is what keeps a stalled pulse off the module
// goroutine and out of the bar's main loop.
func TestSendNeverBlocks(t *testing.T) {
	p := &PulseService{exit: make(chan struct{}), cmds: make(chan command, 2)}

	if err := p.AdjustVolume(5); err != nil {
		t.Fatalf("AdjustVolume: %v", err)
	}
	if err := p.SetMute(true); err != nil {
		t.Fatalf("SetMute: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- p.SetDefaultSink("x") }()
	select {
	case err := <-done:
		if !errors.Is(err, errBusy) {
			t.Fatalf("send on a full queue = %v, want errBusy", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send blocked on a full queue")
	}
}

// Rapid scrolls must accumulate: each step is computed from the value
// the previous one already echoed back, not from the pre-scroll reading.
func TestAdjustVolumeAccumulatesAndClamps(t *testing.T) {
	p := &PulseService{}
	p.state = State{Connected: true, Default: "a", Sinks: []SinkInfo{
		{Name: "a", Volume: 50, Channels: 2},
	}}

	// client is nil, so the round-trip fails fast and only the
	// optimistic update lands - which is exactly the part under test.
	for range 3 {
		p.exec(command{kind: cmdAdjustVolume, value: 5})
	}
	if s, _ := p.State().DefaultSink(); s.Volume != 65 {
		t.Errorf("after 3x +5 from 50, volume = %v, want 65", s.Volume)
	}

	for range 20 {
		p.exec(command{kind: cmdAdjustVolume, value: 10})
	}
	if s, _ := p.State().DefaultSink(); s.Volume != 100 {
		t.Errorf("volume = %v, want a clamp at 100", s.Volume)
	}

	for range 30 {
		p.exec(command{kind: cmdAdjustVolume, value: -10})
	}
	if s, _ := p.State().DefaultSink(); s.Volume != 0 {
		t.Errorf("volume = %v, want a clamp at 0", s.Volume)
	}
}

func TestExecIgnoredWhileDisconnected(t *testing.T) {
	p := &PulseService{}
	p.state = State{Sinks: []SinkInfo{{Name: "a", Volume: 50}}}
	p.exec(command{kind: cmdAdjustVolume, value: 5})
	if p.State().Sinks[0].Volume != 50 {
		t.Error("commands must be dropped while disconnected")
	}
}

// A listener that has not drained yet gets the newest snapshot, not the
// stale one it was already holding: a dropped state is a wrong reading
// on the bar, not merely a late one.
func TestBroadcastKeepsNewest(t *testing.T) {
	p := &PulseService{}
	l := p.IssueListener()

	p.broadcast(State{Connected: true, Default: "a"})
	p.broadcast(State{Connected: true, Default: "b"})

	select {
	case st := <-l:
		if st.Default != "b" {
			t.Errorf("listener got %q, want the newest snapshot", st.Default)
		}
	default:
		t.Fatal("listener got nothing")
	}

	p.RemoveListener(l)
	p.broadcast(State{Connected: true, Default: "c"})
	select {
	case <-l:
		t.Error("a removed listener still received a broadcast")
	default:
	}
}

func TestStopIsIdempotent(t *testing.T) {
	p := &PulseService{}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
