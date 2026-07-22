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
	"time"

	"github.com/codelif/pulseaudio"
)

type PulseService struct {
	running bool
	exit    chan bool
	client  *pulseaudio.Client

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
	if p.running {
		return nil
	}

	client, err := pulseaudio.NewClient("")
	if err != nil {
		return err
	}
	p.client = client

	events, err := client.Events()
	if err != nil {
		return err
	}

	p.exit = make(chan bool)
	p.running = true

	go func() {
		for p.running {
			select {
			case e := <-events:
				if e.Op == pulseaudio.EvChange && (e.Facility == pulseaudio.EvSink || e.Facility == pulseaudio.EvSource) {
					sink, err := p.GetDefaultSinkInfo()
					if err != nil {
						continue
					}
					p.lmu.Lock()
					listeners := slices.Clone(p.listeners)
					p.lmu.Unlock()
					for _, ch := range listeners {
						// Non-blocking: a stale listener must never
						// wedge the broadcast for everyone else.
						select {
						case ch <- sink:
						default:
						}
					}
				}
			case <-p.exit:
				p.running = false
			}
		}
	}()

	return nil
}

func (p *PulseService) GetDefaultSink() (pulseaudio.Sink, error) {
	if !p.running {
		return pulseaudio.Sink{}, fmt.Errorf("pulse service not running")
	}
	serverInfo, err := p.client.ServerInfo()
	if err != nil {
		return pulseaudio.Sink{}, err
	}

	sinks, err := p.client.Sinks()
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
	if !p.running {
		return nil
	}

	select {
	case <-time.After(2 * time.Second):
		return fmt.Errorf("could not stop")
	case p.exit <- true:
		p.running = false
	}

	return nil
}

func (p *PulseService) GetDefaultSinkInfo() (SinkEvent, error) {
	if !p.running {
		return SinkEvent{}, fmt.Errorf("pulse service not running")
	}

	sink, err := p.GetDefaultSink()
	if err != nil {
		return SinkEvent{}, err
	}

	return SinkEvent{
		Sink:   sink.Name,
		Volume: float64(float32(sink.Cvolume[0])/0xffff) * 100,
		Muted:  sink.Muted,
	}, nil
}

// SetSinkVolume sets the sink volume as a percentage (0-100+).
func (p *PulseService) SetSinkVolume(sink string, volume float64) error {
	if !p.running {
		return fmt.Errorf("pulse service not running")
	}
	return p.client.SetSinkVolume(sink, float32(volume/100))
}

// SetSinkMute mutes or unmutes the default sink.
func (p *PulseService) SetSinkMute(sink string, mute bool) error {
	if !p.running {
		return fmt.Errorf("pulse service not running")
	}
	return p.client.SetMute(mute)
}
