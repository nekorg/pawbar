// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package volume

import (
	"bytes"
	"errors"
	"math"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
	"github.com/nekorg/pawbar/internal/services/pulse"
	"github.com/nekorg/pawbar/internal/utils"
)

type VolumeModule struct {
	receive     chan bool
	send        chan modules.Event
	svc         *pulse.PulseService
	sink        string
	Volume      float64
	Muted       bool
	events      <-chan pulse.SinkEvent
	opts        Options
	initialOpts Options
}

func New() modules.Module {
	return &VolumeModule{}
}

func (mod *VolumeModule) Name() string {
	return "volume"
}

func (mod *VolumeModule) Dependencies() []string {
	return []string{"pulse"}
}

func (mod *VolumeModule) Run() (<-chan bool, chan<- modules.Event, error) {
	svc, ok := pulse.Register()
	if !ok {
		return nil, nil, errors.New("pulse service not available")
	}
	if err := svc.Start(); err != nil {
		return nil, nil, err
	}

	sink, err := svc.GetDefaultSinkInfo()
	if err != nil {
		return nil, nil, err
	}

	mod.svc = svc
	mod.sink = sink.Sink
	mod.Volume = sink.Volume
	mod.Muted = sink.Muted

	mod.receive = make(chan bool)
	mod.send = make(chan modules.Event)
	mod.events = svc.IssueListener()
	mod.initialOpts = mod.opts

	go func() {
		for {
			select {
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
				case modules.FocusIn:
					if mod.opts.OnClick.HoverIn(&mod.opts) {
						mod.receive <- true
					}

				case modules.FocusOut:
					if mod.opts.OnClick.HoverOut(&mod.opts) {
						mod.receive <- true
					}
				}

			case e := <-mod.events:
				mod.Volume = e.Volume
				mod.Muted = e.Muted
				mod.receive <- true
			}
		}
	}()

	return mod.receive, mod.send, nil
}

// in case of tui control scollup/down etc

// func (mod *VolumeModule) Change(delta float64) error {
// 	v := mod.Volume + delta
// 	if v < 0 {
// 		v = 0
// 	}
// 	if v > 1 {
// 		v = 1
// 	}
// 	if err := mod.svc.SetSinkVolume(mod.sink, v); err != nil {
// 		return err
// 	}
// 	mod.Volume = v
// 	return nil
// }
//
// func (mod *VolumeModule) ToggleMute() error {
// 	newMute := !mod.Muted
// 	if err := mod.svc.SetSinkMute(mod.sink, newMute); err != nil {
// 		return err
// 	}
// 	mod.Muted = newMute
// 	return nil
// }

func (mod *VolumeModule) Channels() (<-chan bool, chan<- modules.Event) {
	return mod.receive, mod.send
}

func (mod *VolumeModule) Render() []modules.EventCell {
	style := vaxis.Style{}

	if mod.Muted {

		style.Foreground = mod.opts.Muted.Fg.Go()
		style.Background = mod.opts.Muted.Bg.Go()

		text := mod.opts.Muted.MuteFormat
		rch := vaxis.Characters(text)
		r := make([]modules.EventCell, len(rch))

		for i, ch := range rch {
			r[i] = modules.EventCell{
				C:          vaxis.Cell{Character: ch, Style: style},
				Mod:        mod,
				MouseShape: mod.opts.Cursor.Go(),
			}
		}
		return r
	} else {

		style.Foreground = mod.opts.Fg.Go()
		style.Background = mod.opts.Bg.Go()

		vol := int(math.Round(mod.Volume))
		icons := mod.opts.Icons
		idx := utils.Clamp(vol*len(icons)/100, 0, len(icons)-1)
		icon := icons[idx]
		data := struct {
			Icon    string
			Percent int
		}{
			Icon:    string(icon),
			Percent: vol,
		}

		var buf bytes.Buffer
		_ = mod.opts.Format.Execute(&buf, data)
		rch := vaxis.Characters(buf.String())
		r := make([]modules.EventCell, len(rch))
		for i, ch := range rch {
			r[i] = modules.EventCell{
				C:          vaxis.Cell{Character: ch, Style: style},
				Mod:        mod,
				MouseShape: mod.opts.Cursor.Go(),
			}
		}
		return r

	}
}
