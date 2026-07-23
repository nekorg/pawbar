// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package services

import (
	"sync"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
)

// Refcounted service acquisition for the new module runtime. A service
// starts on its first Acquire and stops (after a short linger, to survive
// hot-reload churn) when the last release is called. This file will
// replace the Ensure/ServiceRegistry model once all modules are ported.

// lingerDelay keeps a service alive briefly after its last release so a
// hot reload that removes and re-adds a module doesn't bounce the service.
var lingerDelay = 5 * time.Second

type acquired struct {
	svc    any
	refs   int
	linger *time.Timer
}

var (
	acqMu  sync.Mutex
	acqReg = make(map[string]*acquired)
)

// Acquire returns the named shared service, starting it via factory on
// first use. Call the returned release function when done; it is
// idempotent.
func Acquire[S any](name string, factory func() (S, error)) (S, func(), error) {
	acqMu.Lock()
	defer acqMu.Unlock()

	var zero S
	a, ok := acqReg[name]
	if !ok {
		svc, err := factory()
		if err != nil {
			return zero, nil, err
		}
		a = &acquired{svc: svc}
		acqReg[name] = a
	}
	if a.linger != nil {
		a.linger.Stop()
		a.linger = nil
	}
	a.refs++

	var once sync.Once
	release := func() { once.Do(func() { releaseService(name) }) }
	return a.svc.(S), release, nil
}

func releaseService(name string) {
	acqMu.Lock()
	defer acqMu.Unlock()
	a, ok := acqReg[name]
	if !ok {
		return
	}
	a.refs--
	if a.refs > 0 {
		return
	}
	a.linger = time.AfterFunc(lingerDelay, func() { stopIfIdle(name) })
}

func stopIfIdle(name string) {
	acqMu.Lock()
	a, ok := acqReg[name]
	if !ok || a.refs > 0 {
		acqMu.Unlock()
		return
	}
	delete(acqReg, name)
	acqMu.Unlock()

	if s, ok := a.svc.(interface{ Stop() error }); ok {
		if err := s.Stop(); err != nil {
			logging.Log.Warn().Msgf("service %s: stop: %v", name, err)
		}
	}
}
