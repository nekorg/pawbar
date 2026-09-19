// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package services hands modules the shared, refcounted system services
// they talk to. A service starts on its first Acquire and stops, after a
// short linger to survive hot-reload churn, when the last holder releases.
package services

import (
	"fmt"
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
)

// lingerDelay keeps a service alive briefly after its last release so a
// hot reload that removes and re-adds a module doesn't bounce the service.
var lingerDelay = 5 * time.Second

// acquired is one service in the registry. Handing it to the release
// closure rather than the name is what makes a late release safe: once an
// entry is dead it can never be confused with the instance that replaced
// it under the same name.
type acquired struct {
	name string

	// ready closes once svc/err are set. A second caller arriving while
	// the factory runs waits on it instead of starting a second service.
	ready chan struct{}
	svc   any
	err   error

	refs   int
	linger *time.Timer
	// dead marks an entry that has left the registry, either because its
	// factory failed or because it was stopped. Releases against it, and
	// linger timers still holding it, are no-ops.
	dead bool
}

var (
	acqMu  sync.Mutex
	acqReg = make(map[string]*acquired)
)

// Acquire returns the named shared service, starting it via factory on
// first use. Call the returned release function when done; it is
// idempotent, and safe to call after the service has already been stopped.
//
// factory runs without the registry lock held: starting a service can mean
// a blocking bus connect, and holding the one global lock across that would
// stall every other module's acquisition behind it.
func Acquire[S any](name string, factory func() (S, error)) (S, func(), error) {
	var zero S
	for {
		acqMu.Lock()
		a, ok := acqReg[name]
		if !ok {
			a = &acquired{name: name, ready: make(chan struct{})}
			acqReg[name] = a
			acqMu.Unlock()

			svc, err := factory()

			acqMu.Lock()
			a.svc, a.err = svc, err
			if err != nil {
				// A failed start is not cached: the next module to ask
				// gets a fresh attempt rather than the old error.
				a.dead = true
				delete(acqReg, name)
			} else {
				a.refs = 1
			}
			acqMu.Unlock()
			close(a.ready)

			if err != nil {
				return zero, nil, err
			}
			return handOut[S](a)
		}
		acqMu.Unlock()

		// Someone else is starting it, or already has.
		<-a.ready

		acqMu.Lock()
		if a.dead {
			// Its factory failed, or it was stopped between the lookup and
			// now. Go round and start one ourselves.
			acqMu.Unlock()
			continue
		}
		if a.linger != nil {
			a.linger.Stop()
			a.linger = nil
		}
		a.refs++
		acqMu.Unlock()
		return handOut[S](a)
	}
}

// handOut types a held entry for its caller, giving the ref back when the
// caller asked for a type this service is not.
func handOut[S any](a *acquired) (S, func(), error) {
	svc, ok := a.svc.(S)
	if !ok {
		var zero S
		release(a)
		return zero, nil, fmt.Errorf("service %q is a %T, not a %T", a.name, a.svc, zero)
	}
	var once sync.Once
	return svc, func() { once.Do(func() { release(a) }) }, nil
}

func release(a *acquired) {
	acqMu.Lock()
	defer acqMu.Unlock()
	if a.dead {
		return
	}
	a.refs--
	if a.refs > 0 {
		return
	}
	a.linger = time.AfterFunc(lingerDelay, func() { stopIfIdle(a) })
}

func stopIfIdle(a *acquired) {
	acqMu.Lock()
	if a.dead || a.refs > 0 {
		acqMu.Unlock()
		return
	}
	a.dead = true
	// Only clear the slot if this entry is still the one in it.
	if acqReg[a.name] == a {
		delete(acqReg, a.name)
	}
	acqMu.Unlock()

	if s, ok := a.svc.(interface{ Stop() error }); ok {
		if err := s.Stop(); err != nil {
			logging.Log.Warn().Msgf("service %s: stop: %v", a.name, err)
		}
	}
}
