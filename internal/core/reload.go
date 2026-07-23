// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package core

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"github.com/rs/zerolog"
)

const reloadDebounce = 250 * time.Millisecond

// WatchConfig watches the directory containing path (editors replace files
// by rename, so watching the file itself would go stale) and delivers a
// debounced signal whenever the config file may have changed. The watcher
// stops when done closes.
func WatchConfig(done <-chan struct{}, path string, log zerolog.Logger) (<-chan struct{}, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filepath.Dir(path)); err != nil {
		w.Close()
		return nil, err
	}
	base := filepath.Base(path)
	out := make(chan struct{}, 1)

	go func() {
		defer w.Close()
		var timer *time.Timer
		var timerC <-chan time.Time
		for {
			select {
			case <-done:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != base {
					continue
				}
				if timer == nil {
					timer = time.NewTimer(reloadDebounce)
					timerC = timer.C
				} else {
					timer.Reset(reloadDebounce)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Warn().Msgf("config watch: %v", err)
			case <-timerC:
				timer, timerC = nil, nil
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out, nil
}

// SlotRef addresses one module slot.
type SlotRef struct {
	Side  Side
	Index int
}

// Restarts delivers slots that requested a restart (a failed OnConfig).
// The main loop answers each with RestartSlot.
func (e *Engine) Restarts() <-chan SlotRef { return e.restarts }

func (e *Engine) requestRestart(r *runner) {
	select {
	case e.restarts <- SlotRef{r.side, r.idx}:
	default:
	}
}

// RestartSlot stops one slot's runner and starts a fresh one with the same
// configuration. Main goroutine only.
func (e *Engine) RestartSlot(s SlotRef) {
	old := e.runnerAt(s.Side, s.Index)
	if old == nil {
		return
	}
	e.clearPointer(old)
	old.stop()
	nr := newRunner(e, s.Side, s.Index, old.in())
	e.mu.Lock()
	e.sides[s.Side][s.Index] = nr
	e.mu.Unlock()
	nr.start()
}

// Reload applies a newly compiled bar, positionally diffing each side
// against the running slots:
//
//   - same name, same hash  → keep the runner; swap the compiled tables in
//     place (they may embed a changed theme)
//   - same name, new hash   → in-place reconfigure when the module
//     implements Reconfigurer, restart otherwise
//   - anything else         → stop the old slot, start the new one
//
// Reordering entries therefore restarts them; positional diffing is the
// predictable trade for short bar lists. Main goroutine only.
func (e *Engine) Reload(bar *config.Bar) {
	e.PointerLeft()
	newInsts := [3][]*config.Instance{bar.Left, bar.Middle, bar.Right}
	var starts []*runner

	for s := range e.sides {
		old, insts := e.sides[s], newInsts[s]
		next := make([]*runner, len(insts))
		for i, inst := range insts {
			var o *runner
			if i < len(old) {
				o = old[i]
			}
			switch {
			case o != nil && canKeep(o, inst):
				o.applyConfig(inst, false)
				next[i] = o
			case o != nil && canReconfigure(o, inst):
				o.applyConfig(inst, true)
				next[i] = o
			default:
				if o != nil {
					o.stop()
				}
				nr := newRunner(e, Side(s), i, inst)
				next[i] = nr
				starts = append(starts, nr)
			}
		}
		for i := len(insts); i < len(old); i++ {
			old[i].stop()
		}
		e.mu.Lock()
		e.sides[s] = next
		e.mu.Unlock()
	}

	for _, r := range starts {
		r.start()
	}
}

func canKeep(o *runner, inst *config.Instance) bool {
	cur := o.in()
	return cur.Name == inst.Name && cur.Hash == inst.Hash &&
		cur.Err == nil && inst.Err == nil && !o.broken.Load()
}

func canReconfigure(o *runner, inst *config.Instance) bool {
	cur := o.in()
	if cur.Name != inst.Name || cur.Err != nil || inst.Err != nil || o.broken.Load() {
		return false
	}
	_, ok := o.mod.(module.Reconfigurer)
	return ok
}
