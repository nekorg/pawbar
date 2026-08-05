// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package backlight

import (
	"time"

	"github.com/nekorg/pawbar/internal/services/ddc"
	"github.com/nekorg/pawbar/pkg/module"
)

// The DDC/CI backend: a thin adapter over the ddc service.
//
// Everything expensive — the open transport, the write coalescing, the
// re-read poll — belongs to the service's per-display worker. This type only
// holds the last snapshot it published and forwards Set requests, so nothing
// here can block the module goroutine.

type ddcBackend struct {
	svc     *ddc.Service
	release func()

	display ddc.Display
	poll    time.Duration

	cur, max int
	pct      int

	// onFail lets `auto` retreat to sysfs when the display turns out not
	// to speak DDC/CI after all.
	onFail func(error)
}

func newDDCBackend(d ddc.Display, poll time.Duration) *ddcBackend {
	return &ddcBackend{display: d, poll: poll}
}

func (b *ddcBackend) Name() string { return "ddc" }

func (b *ddcBackend) Raw() (now, max int) { return b.cur, b.max }

func (b *ddcBackend) Pct() int { return b.pct }

func (b *ddcBackend) Start(ctx *module.Ctx) error {
	svc, release, err := ddc.Acquire()
	if err != nil {
		return err
	}
	b.svc, b.release = svc, release

	module.On(ctx, svc.Watch(b.display, b.poll), func(ev ddc.Event) {
		if !ev.Ready {
			if b.onFail != nil && ev.Err != nil {
				b.onFail(ev.Err)
			}
			return
		}
		b.cur, b.max, b.pct = int(ev.Cur), int(ev.Max), ev.Pct
	})
	return nil
}

func (b *ddcBackend) Set(pct int) error {
	// Returns immediately: the service renders this value now and puts it
	// on the bus when the bus is next free.
	b.svc.Set(b.display.Connector, pct)
	b.pct = pct
	if b.max > 0 {
		b.cur = (pct*b.max + 50) / 100
	}
	return nil
}

func (b *ddcBackend) Stop() {
	if b.release != nil {
		b.release()
		b.release = nil
	}
}
