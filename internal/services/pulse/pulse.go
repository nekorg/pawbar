// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package pulse

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codelif/pulseaudio"
	"github.com/nekorg/pawbar/internal/logging"
)

// One goroutine owns the client. Everything else reads the cached
// snapshot or drops a command in the queue; nothing outside this file
// ever touches the socket, because a pulse round-trip on the module
// goroutine takes the whole bar down with it when the server goes away.

var (
	backoffMin = 1 * time.Second
	backoffMax = 30 * time.Second

	// callTimeout bounds a single round-trip. A socket that is open but
	// silent is indistinguishable from a healthy one until this fires.
	callTimeout = 2 * time.Second

	// coalesce delays the refresh after an event so a burst costs one
	// ServerInfo+Sinks pass instead of one per event.
	coalesce = 30 * time.Millisecond

	// resyncEvery re-reads state even with no events, in case one was
	// missed.
	resyncEvery = 30 * time.Second
)

var errBusy = errors.New("pulse: command queue full")

// SinkInfo is one output device.
type SinkInfo struct {
	Name        string
	Description string // as the server reports it
	Label       string // short name, for display
	Index       uint32
	Volume      float64 // 0-100
	Muted       bool
	Channels    int
	// Available is false only when the device says it has nothing
	// plugged in: an unused HDMI port, a headphone jack with no
	// headphones.
	Available bool
}

// State is a complete snapshot, never a delta.
type State struct {
	Connected bool
	Default   string
	Sinks     []SinkInfo
}

// DefaultSink returns the sink State.Default names.
func (s State) DefaultSink() (SinkInfo, bool) {
	for _, sk := range s.Sinks {
		if sk.Name == s.Default {
			return sk, true
		}
	}
	return SinkInfo{}, false
}

type command struct {
	kind  cmdKind
	name  string
	value float64
	mute  bool
}

type cmdKind int

const (
	cmdAdjustVolume cmdKind = iota
	cmdSetMute
	cmdSetDefaultSink
	cmdRefresh
)

type PulseService struct {
	exit     chan struct{}
	stopOnce sync.Once
	cmds     chan command

	client *pulseaudio.Client // worker goroutine only

	smu   sync.RWMutex
	state State

	lmu       sync.Mutex
	listeners []chan State
}

func (p *PulseService) Name() string { return "pulse" }

// State returns the last known snapshot. Never blocks, never talks to
// the server.
func (p *PulseService) State() State {
	p.smu.RLock()
	defer p.smu.RUnlock()
	return p.state
}

func (p *PulseService) IssueListener() <-chan State {
	l := make(chan State, 1)
	p.lmu.Lock()
	p.listeners = append(p.listeners, l)
	p.lmu.Unlock()

	return l
}

// RemoveListener detaches a channel previously issued by IssueListener so
// the service stops broadcasting to it (hot reload removes modules while
// the service keeps running for other subscribers).
func (p *PulseService) RemoveListener(l <-chan State) {
	p.lmu.Lock()
	defer p.lmu.Unlock()
	for i, ch := range p.listeners {
		if (<-chan State)(ch) == l {
			p.listeners = append(p.listeners[:i], p.listeners[i+1:]...)
			return
		}
	}
}

// Start never fails. The server may not be up yet; that is the worker's
// problem, not a reason to kill every module that wants volume.
func (p *PulseService) Start() error {
	p.exit = make(chan struct{})
	p.cmds = make(chan command, 32)
	go p.run()
	return nil
}

func (p *PulseService) Stop() error {
	if p.exit == nil {
		return nil
	}
	p.stopOnce.Do(func() { close(p.exit) })
	return nil
}

func (p *PulseService) run() {
	defer logging.Recover("pulse.run")

	defer func() {
		if p.client != nil {
			p.client.Close()
		}
	}()

	events := p.connect()
	if events == nil {
		return
	}

	refresh := time.NewTimer(time.Hour)
	refresh.Stop()
	defer refresh.Stop()
	pending := false

	resync := time.NewTicker(resyncEvery)
	defer resync.Stop()

	p.refresh()

	for {
		select {
		case <-p.exit:
			return

		case c := <-p.cmds:
			p.exec(c)

		case e, ok := <-events:
			if !ok {
				if events = p.connect(); events == nil {
					return
				}
				pending = false
				p.refresh()
				continue
			}
			if interesting(e) && !pending {
				pending = true
				refresh.Reset(coalesce)
			}

		case <-refresh.C:
			pending = false
			p.refresh()

		case <-resync.C:
			p.refresh()
		}
	}
}

// interesting picks the events worth a re-read. Sink events (any op)
// keep the device list current; server events are how a default-sink
// switch from pavucontrol arrives; dropping them was why the module sat
// on a stale sink until something else happened to fire.
func interesting(e pulseaudio.Event) bool {
	return e.Facility == pulseaudio.EvSink || e.Facility == pulseaudio.EvServer
}

// connect dials with backoff until it works or the service stops. It
// never gives up: pawbar routinely starts before pipewire-pulse does.
func (p *PulseService) connect() <-chan pulseaudio.Event {
	if p.client != nil {
		p.client.Close()
		p.client = nil
		logging.Log.Warn().Msg("pulse: connection lost; reconnecting")
	}
	p.publish(State{})

	backoff := backoffMin
	first := true
	for {
		select {
		case <-p.exit:
			return nil
		default:
		}

		client, err := pulseaudio.NewClient("")
		if err == nil {
			var events <-chan pulseaudio.Event
			events, err = client.Events()
			if err == nil {
				p.client = client
				if !first {
					logging.Log.Info().Msg("pulse: connected")
				}
				return events
			}
			client.Close()
		}

		if first {
			logging.Log.Info().Msgf("pulse: connect: %v (retrying)", err)
			first = false
		} else {
			logging.Log.Debug().Msgf("pulse: connect: %v (retry in %v)", err, backoff)
		}
		select {
		case <-p.exit:
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, backoffMax)
	}
}

// call runs one round-trip with a deadline. A pulse request has no
// cancellation, so on timeout the connection is declared dead and closed,
// which is also what releases the orphaned goroutine.
func call[T any](p *PulseService, fn func(*pulseaudio.Client) (T, error)) (T, error) {
	var zero T
	c := p.client
	if c == nil {
		return zero, pulseaudio.ErrConnClosed
	}

	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := fn(c)
		done <- result{v, err}
	}()

	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(callTimeout):
		logging.Log.Warn().Msg("pulse: request timed out; dropping the connection")
		c.Close()
		return zero, pulseaudio.ErrConnClosed
	}
}

// refresh re-reads the world and broadcasts it.
func (p *PulseService) refresh() {
	st, err := p.snapshot()
	if err != nil {
		logging.Log.Debug().Msgf("pulse: refresh: %v", err)
		return
	}
	p.publish(st)
}

func (p *PulseService) snapshot() (State, error) {
	info, err := call(p, func(c *pulseaudio.Client) (*pulseaudio.Server, error) { return c.ServerInfo() })
	if err != nil {
		return State{}, err
	}
	sinks, err := call(p, func(c *pulseaudio.Client) ([]pulseaudio.Sink, error) { return c.Sinks() })
	if err != nil {
		return State{}, err
	}

	st := State{Connected: true, Default: info.DefaultSink}
	for _, s := range sinks {
		st.Sinks = append(st.Sinks, SinkInfo{
			Name:        s.Name,
			Description: s.Description,
			Label:       sinkLabel(s),
			Index:       s.Index,
			Volume:      volumePct(s.Cvolume),
			Muted:       s.Muted,
			Channels:    len(s.Cvolume),
			Available:   sinkAvailable(s),
		})
	}
	sort.Slice(st.Sinks, func(i, j int) bool { return st.Sinks[i].Index < st.Sinks[j].Index })
	return st, nil
}

// sinkLabel picks something short enough to read in a menu.
// Description is the card name with the profile glued on the end -
// "Core Ultra 200H/200V Series Processors HD Audio HDMI / DisplayPort 1
// Output" - which is useless in a list.
func sinkLabel(s pulseaudio.Sink) string {
	// pipewire names the node after what it actually is: the monitor's
	// edid name, "Speaker", the bluetooth device.
	if nick := strings.TrimSpace(s.PropList["node.nick"]); nick != "" {
		return nick
	}
	// no nick (classic pulseaudio): drop the card prefix instead of
	// reaching for device.profile.description, which is the generic half
	// on bluetooth ("High Fidelity Playback (A2DP Sink)").
	if card := s.PropList["device.description"]; card != "" {
		if rest := strings.TrimSpace(strings.TrimPrefix(s.Description, card)); rest != "" && rest != s.Description {
			return rest
		}
	}
	if s.Description != "" {
		return s.Description
	}
	return s.Name
}

// sinkAvailable is false only when every port says NO. Unknown is a
// device with no jack detection - a built-in speaker, a usb dac - which
// is not the same thing as unplugged.
func sinkAvailable(s pulseaudio.Sink) bool {
	// virtual sinks (null, combine, easyeffects) have no ports at all.
	if len(s.Ports) == 0 {
		return true
	}
	for _, p := range s.Ports {
		if p.Available != pulseaudio.PortAvailableNo {
			return true
		}
	}
	return false
}

// volumePct reads channel 0; per-channel imbalance is not modelled.
func volumePct(v pulseaudio.Cvolume) float64 {
	if len(v) == 0 {
		return 0
	}
	return float64(v[0]) / 0xffff * 100
}

func (p *PulseService) publish(st State) {
	p.smu.Lock()
	p.state = st
	p.smu.Unlock()
	p.broadcast(st)
}

func (p *PulseService) broadcast(st State) {
	p.lmu.Lock()
	listeners := slices.Clone(p.listeners)
	p.lmu.Unlock()
	for _, ch := range listeners {
		// Keep the newest snapshot rather than dropping it: a lost
		// state is a wrong reading on the bar, not just a late one.
		select {
		case ch <- st:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- st:
			default:
			}
		}
	}
}

func (p *PulseService) send(c command) error {
	select {
	case p.cmds <- c:
		return nil
	case <-p.exit:
		return fmt.Errorf("pulse service not running")
	default:
		return errBusy
	}
}

// AdjustVolume changes the default sink's volume by delta percent.
func (p *PulseService) AdjustVolume(delta float64) error {
	return p.send(command{kind: cmdAdjustVolume, value: delta})
}

// SetMute mutes or unmutes the default sink.
func (p *PulseService) SetMute(mute bool) error {
	return p.send(command{kind: cmdSetMute, mute: mute})
}

// SetDefaultSink makes name the default sink.
func (p *PulseService) SetDefaultSink(name string) error {
	return p.send(command{kind: cmdSetDefaultSink, name: name})
}

// Resync forces a re-read, for when events cannot be trusted to have
// arrived (resume from suspend).
func (p *PulseService) Resync() error {
	return p.send(command{kind: cmdRefresh})
}

func (p *PulseService) exec(c command) {
	st := p.State()
	if !st.Connected {
		return
	}

	switch c.kind {
	case cmdRefresh:
		p.refresh()

	case cmdAdjustVolume:
		sink, ok := st.DefaultSink()
		if !ok {
			return
		}
		// Compute against the cached value and echo it back
		// immediately: a fast scroll must accumulate, not recompute
		// every step from the same pre-scroll reading.
		v := math.Max(0, math.Min(100, sink.Volume+c.value))
		p.optimistic(sink.Name, func(s *SinkInfo) { s.Volume = v })
		if _, err := call(p, func(cl *pulseaudio.Client) (struct{}, error) {
			return struct{}{}, cl.SetSinkVolumeAll(sink.Name, float32(v/100), sink.Channels)
		}); err != nil {
			logging.Log.Error().Msgf("pulse: set volume: %v", err)
		}

	case cmdSetMute:
		sink, ok := st.DefaultSink()
		if !ok {
			return
		}
		p.optimistic(sink.Name, func(s *SinkInfo) { s.Muted = c.mute })
		if _, err := call(p, func(cl *pulseaudio.Client) (struct{}, error) {
			return struct{}{}, cl.SetSinkMuteByName(sink.Name, c.mute)
		}); err != nil {
			logging.Log.Error().Msgf("pulse: set mute: %v", err)
		}

	case cmdSetDefaultSink:
		if _, err := call(p, func(cl *pulseaudio.Client) (struct{}, error) {
			return struct{}{}, cl.SetDefaultSink(c.name)
		}); err != nil {
			logging.Log.Error().Msgf("pulse: set default sink: %v", err)
			return
		}
		p.refresh()
	}
}

// optimistic applies a change to the cached snapshot before the server
// confirms it, so the bar reacts on the same frame as the click.
func (p *PulseService) optimistic(sink string, fn func(*SinkInfo)) {
	p.smu.Lock()
	st := p.state
	st.Sinks = slices.Clone(st.Sinks)
	for i := range st.Sinks {
		if st.Sinks[i].Name == sink {
			fn(&st.Sinks[i])
			break
		}
	}
	p.state = st
	p.smu.Unlock()
	p.broadcast(st)
}
