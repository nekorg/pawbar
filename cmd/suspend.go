// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)


type ResumeEvent struct{ Source string }

func watchResume(ctx context.Context) <-chan ResumeEvent {
	out := make(chan ResumeEvent, 1)

	go func() {
		defer close(out)

		bus, err := dbus.ConnectSystemBus()
		if err != nil {
			return
		}
		defer bus.Close()

		logindOpts := []dbus.MatchOption{
			dbus.WithMatchSender("org.freedesktop.login1"),
			dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
			dbus.WithMatchMember("PrepareForSleep"),
		}
		upowerOpts := []dbus.MatchOption{
			dbus.WithMatchSender("org.freedesktop.UPower"),
			dbus.WithMatchInterface("org.freedesktop.UPower"),
			dbus.WithMatchMember("Resuming"),
		}
		
		// TODO: properly handle errors
		_ = bus.AddMatchSignal(logindOpts...)
		_ = bus.AddMatchSignal(upowerOpts...)
		defer func() {
			_ = bus.RemoveMatchSignal(logindOpts...)
			_ = bus.RemoveMatchSignal(upowerOpts...)
		}()

		sigC := make(chan *dbus.Signal, 8)
		bus.Signal(sigC)
		defer bus.RemoveSignal(sigC)

		var last time.Time
		const debounce = 250 * time.Millisecond

		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigC:
				if sig == nil {
					return
				}

				now := time.Now()
				if !last.IsZero() && now.Sub(last) < debounce {
					continue
				}

				switch sig.Name {
				case "org.freedesktop.login1.Manager.PrepareForSleep":
					// Body: [bool asleep]
					if len(sig.Body) == 1 {
						if asleep, _ := sig.Body[0].(bool); asleep {
							continue // going to sleep
						}
						last = now
						select { case out <- ResumeEvent{Source: "login1"}: default: }
					}
				case "org.freedesktop.UPower.Resuming":
					last = now
					select { case out <- ResumeEvent{Source: "UPower"}: default: }
				}
			}
		}
	}()

	return out
}
