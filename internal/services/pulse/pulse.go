// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package pulse

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codelif/pulseaudio"
	"github.com/nekorg/pawbar/internal/logging"
)

const maxBackoff = 30 * time.Second

type PulseService struct {
	running atomic.Bool
	exit    chan struct{}

	cmu    sync.RWMutex
	client *pulseaudio.Client

	lmu       sync.Mutex
	listeners []chan SinkEvent
}

type SinkEvent struct {
	Sink   string
	Volume float64
	Muted  bool
}

func (p *PulseService) Name() string { return "pulse" }

func (p *PulseService) IssueListener() <-chan SinkEvent {
	l := make(chan SinkEvent, 10)
	p.lmu.Lock()
	p.listeners = append(p.listeners, l)
	p.lmu.Unlock()

	return l
}

// RemoveListener detaches a channel previously issued by IssueListener so
// the service stops broadcasting to it (hot reload removes modules while
// the service keeps running for other subscribers).
func (p *PulseService) RemoveListener(l <-chan SinkEvent) {
	p.lmu.Lock()
	defer p.lmu.Unlock()
	for i, ch := range p.listeners {
		if (<-chan SinkEvent)(ch) == l {
			p.listeners = append(p.listeners[:i], p.listeners[i+1:]...)
			return
		}
	}
}

func (p *PulseService) Start() error {
	if p.running.Load() {
		return nil
	}

	client, err := pulseaudio.NewClient("")
	if err != nil {
		return err
	}

	events, err := client.Events()
	if err != nil {
		client.Close()
		return err
	}

	p.client = client
	p.exit = make(chan struct{})
	p.running.Store(true)

	go p.loop(events)

	return nil
}

func (p *PulseService) loop(events <-chan pulseaudio.Event) {
	defer logging.Recover("pulse.loop")
	for {
		select {
		case <-p.exit:
			return
		case e, ok := <-events:
			if !ok {
				// PulseAudio went away and closed the event channel.
				events = p.reconnect()
				if events == nil {
					return
				}
				continue
			}
			if e.Op == pulseaudio.EvChange && (e.Facility == pulseaudio.EvSink || e.Facility == pulseaudio.EvSource) {
				sink, err := p.GetDefaultSinkInfo()
				if err != nil {
					continue
				}
				p.broadcast(sink)
			}
		}
	}
}

// reconnect re-establishes the PulseAudio connection with backoff,
// returning the fresh event channel, or nil when the service stops
// first.
func (p *PulseService) reconnect() <-chan pulseaudio.Event {
	logging.Log.Warn().Msg("pulse: connection lost; reconnecting")
	backoff := time.Second
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
				p.cmu.Lock()
				old := p.client
				p.client = client
				p.cmu.Unlock()
				if old != nil {
					old.Close()
				}
				logging.Log.Info().Msg("pulse: reconnected")
				if sink, serr := p.GetDefaultSinkInfo(); serr == nil {
					p.broadcast(sink)
				}
				return events
			}
			client.Close()
		}

		logging.Log.Error().Msgf("pulse: reconnect: %v (retry in %v)", err, backoff)
		select {
		case <-p.exit:
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (p *PulseService) broadcast(sink SinkEvent) {
	p.lmu.Lock()
	listeners := slices.Clone(p.listeners)
	p.lmu.Unlock()
	for _, ch := range listeners {
		// Non-blocking: a stale listener must never wedge the
		// broadcast for everyone else.
		select {
		case ch <- sink:
		default:
		}
	}
}

// c returns the current client; it changes across reconnects.
func (p *PulseService) c() *pulseaudio.Client {
	p.cmu.RLock()
	defer p.cmu.RUnlock()
	return p.client
}

func (p *PulseService) GetDefaultSink() (pulseaudio.Sink, error) {
	if !p.running.Load() {
		return pulseaudio.Sink{}, fmt.Errorf("pulse service not running")
	}
	client := p.c()
	if client == nil {
		return pulseaudio.Sink{}, fmt.Errorf("pulse service not running")
	}
	serverInfo, err := client.ServerInfo()
	if err != nil {
		return pulseaudio.Sink{}, err
	}

	sinks, err := client.Sinks()
	if err != nil {
		return pulseaudio.Sink{}, err
	}

	for _, sink := range sinks {
		if sink.Name != serverInfo.DefaultSink {
			continue
		}

		return sink, nil
	}

	return pulseaudio.Sink{}, fmt.Errorf("default sink '%s' not found", serverInfo.DefaultSink)
}

func (p *PulseService) Stop() error {
	if !p.running.Load() {
		return nil
	}
	p.running.Store(false)
	close(p.exit)

	p.cmu.Lock()
	client := p.client
	p.client = nil
	p.cmu.Unlock()
	if client != nil {
		client.Close()
	}

	return nil
}

func (p *PulseService) GetDefaultSinkInfo() (SinkEvent, error) {
	if !p.running.Load() {
		return SinkEvent{}, fmt.Errorf("pulse service not running")
	}

	sink, err := p.GetDefaultSink()
	if err != nil {
		return SinkEvent{}, err
	}

	volume := 0.0
	if len(sink.Cvolume) > 0 {
		volume = float64(float32(sink.Cvolume[0])/0xffff) * 100
	}

	return SinkEvent{
		Sink:   sink.Name,
		Volume: volume,
		Muted:  sink.Muted,
	}, nil
}

// SetSinkVolume sets the sink volume as a percentage (0-100+).
func (p *PulseService) SetSinkVolume(sink string, volume float64) error {
	if !p.running.Load() {
		return fmt.Errorf("pulse service not running")
	}
	client := p.c()
	if client == nil {
		return fmt.Errorf("pulse service not running")
	}
	return client.SetSinkVolume(sink, float32(volume/100))
}

// SetSinkMute mutes or unmutes the default sink. The underlying client
// can only address the default sink, so any other name is an error
// rather than being silently redirected.
func (p *PulseService) SetSinkMute(sink string, mute bool) error {
	if !p.running.Load() {
		return fmt.Errorf("pulse service not running")
	}
	client := p.c()
	if client == nil {
		return fmt.Errorf("pulse service not running")
	}
	if sink != "" {
		info, err := client.ServerInfo()
		if err != nil {
			return err
		}
		if sink != info.DefaultSink {
			return fmt.Errorf("can only mute the default sink (%s), not %s", info.DefaultSink, sink)
		}
	}
	return client.SetMute(mute)
}
