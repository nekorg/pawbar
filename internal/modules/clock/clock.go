// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package clock

import (
	"strings"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/itchyny/timefmt-go"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/menus/calendar"
	"github.com/nekorg/pawbar/internal/modules"
)

type ClockModule struct {
	receive chan bool
	send    chan modules.Event

	opts        Options
	initialOpts Options

	currentTickerInterval time.Duration
	initialTimer          *time.Timer
	ticker                *time.Ticker
}

func (mod *ClockModule) Dependencies() []string {
	return nil
}

func (mod *ClockModule) Run() (<-chan bool, chan<- modules.Event, error) {
	mod.receive = make(chan bool)
	mod.send = make(chan modules.Event)
	mod.initialOpts = mod.opts

	go func() {
		mod.startTickerAligned(mod.effectiveTick())
		defer mod.stopTickers()
		for {
			var timerC <-chan time.Time
			if mod.initialTimer != nil {
				timerC = mod.initialTimer.C
			}
			var tickerC <-chan time.Time
			if mod.ticker != nil {
				tickerC = mod.ticker.C
			}
			select {
			case <-timerC:
				mod.receive <- true
				mod.startSteadyTicker()
			case <-tickerC:
				mod.receive <- true
			case e := <-mod.send:
				switch ev := e.VaxisEvent.(type) {
				case vaxis.Mouse:
					if ev.EventType != vaxis.EventPress {
						break
					}
					btn := config.ButtonName(ev)
					if mod.opts.OnClick.Dispatch(btn, &mod.initialOpts, &mod.opts) {
						mod.receive <- true
					}
					mod.ensureTickInterval()
					switch ev.Button {
					case vaxis.MouseRightButton:
						go calendar.LaunchMenu(ev.XPixel/2, ev.YPixel/2)
					}

				case modules.FocusIn:
					if mod.opts.OnClick.HoverIn(&mod.opts) {
						mod.receive <- true
					}
					mod.ensureTickInterval()

				case modules.FocusOut:
					if mod.opts.OnClick.HoverOut(&mod.opts) {
						mod.receive <- true
					}
					mod.ensureTickInterval()

				case modules.SystemWake:
					mod.startTickerAligned(mod.effectiveTick())
				}
			}
		}
	}()

	return mod.receive, mod.send, nil
}

func (mod *ClockModule) ensureTickInterval() {
	interval := mod.effectiveTick()
	if interval != mod.currentTickerInterval {
		mod.startTickerAligned(interval)
	}
}

func (mod *ClockModule) effectiveTick() time.Duration {
	if !mod.opts.AutoTick {
		return mod.opts.Tick.Go()
	}

	return formatTick(mod.opts.Format)
}

func formatTick(format string) time.Duration {
	if containsAny(format, "%L", "%N", "%f") {
		return time.Millisecond
	}
	if strings.Contains(format, "%S") {
		return time.Second
	}
	if strings.Contains(format, "%M") {
		return time.Minute
	}
	if containsAny(format, "%H", "%I", "%k", "%l") {
		return time.Hour
	}
	if containsAny(format, "%d", "%e", "%j", "%a", "%A", "%u", "%w") {
		return 24 * time.Hour
	}
	if containsAny(format, "%m", "%b", "%B", "%y", "%Y") {
		return 24 * time.Hour
	}

	return time.Second
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func (mod *ClockModule) startTickerAligned(interval time.Duration) {
	mod.stopTickers()
	mod.currentTickerInterval = interval

	if interval <= 0 {
		interval = time.Second
		mod.currentTickerInterval = interval
	}

	mod.initialTimer = time.NewTimer(time.Until(nextBoundary(time.Now(), interval)))
}

func (mod *ClockModule) startSteadyTicker() {
	mod.stopInitialTimer()
	mod.ticker = time.NewTicker(mod.currentTickerInterval)
}

func nextBoundary(now time.Time, interval time.Duration) time.Time {
	return now.Truncate(interval).Add(interval)
}

func (mod *ClockModule) stopTickers() {
	mod.stopInitialTimer()
	if mod.ticker != nil {
		mod.ticker.Stop()
		mod.ticker = nil
	}
}

func (mod *ClockModule) stopInitialTimer() {
	if mod.initialTimer == nil {
		return
	}
	if !mod.initialTimer.Stop() {
		select {
		case <-mod.initialTimer.C:
		default:
		}
	}
	mod.initialTimer = nil
}

func (mod *ClockModule) Render() []modules.EventCell {
	var s vaxis.Style
	s.Foreground = mod.opts.Fg.Go()
	s.Background = mod.opts.Bg.Go()

	rch := vaxis.Characters(timefmt.Format(time.Now(), mod.opts.Format))
	r := make([]modules.EventCell, len(rch))
	for i, ch := range rch {
		r[i] = modules.EventCell{
			C: vaxis.Cell{
				Character: ch,
				Style:     s,
			},
			Metadata:   "",
			Mod:        mod,
			MouseShape: mod.opts.Cursor.Go(),
		}
	}
	return r
}

func (mod *ClockModule) Channels() (<-chan bool, chan<- modules.Event) {
	return mod.receive, mod.send
}

func (mod *ClockModule) Name() string {
	return "clock"
}
