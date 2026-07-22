// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

// Host is the runtime side of a module context. It is implemented by
// pawbar's core; module authors never touch it.
type Host interface {
	Name() string
	Logf(format string, args ...any)
	// SetState flips a state on the module instance and schedules a
	// restyle/redraw.
	SetState(name string, on bool)
	// States returns the currently active state names.
	States() []string
	// Block returns the style/format Block resolved for the currently
	// active states.
	Block() Block
	// Refresh schedules a redraw outside of the automatic
	// after-every-hook redraw.
	Refresh()
	// Go runs fn on a runtime-tracked helper goroutine, for blocking
	// one-shots (launching a menu, an exec). fn must not touch module
	// state; deliver results back through a source or verb.
	Go(fn func())
	// SubscriptionAdded is called whenever On registers a subscription;
	// the runtime opens it (immediately if the module is already
	// running).
	SubscriptionAdded(s *Subscription)
}

// Ctx is a module instance's handle to the runtime. It is only valid on
// the module goroutine (inside Init, hooks, and source handlers).
type Ctx struct {
	host  Host
	opts  any
	verbs map[string]func(VerbArgs) error
}

// NewCtx builds a module context. Runtime use.
func NewCtx(host Host, opts any) *Ctx {
	return &Ctx{host: host, opts: opts, verbs: make(map[string]func(VerbArgs) error)}
}

// Name returns the module's registered name.
func (c *Ctx) Name() string { return c.host.Name() }

// Log writes to the pawbar log, prefixed with the module name.
func (c *Ctx) Log(format string, args ...any) { c.host.Logf(format, args...) }

// Options returns the module's resolved options struct: the pointer type
// Def.Options returned, populated with theme defaults, the user's config
// and any active state overrides. Type-assert it once per use:
//
//	opts := ctx.Options().(*Options)
//
// The runtime swaps the value when active states or config change (see
// StateObserver / Reconfigurer), so don't cache it across hooks.
func (c *Ctx) Options() any { return c.opts }

// SetOptions replaces the resolved options. Runtime use.
func (c *Ctx) SetOptions(opts any) { c.opts = opts }

// SetState turns a declared condition state (or user state) on or off,
// re-resolving styling and options and redrawing as needed.
func (c *Ctx) SetState(name string, on bool) { c.host.SetState(name, on) }

// States returns the active state names.
func (c *Ctx) States() []string { return c.host.States() }

// ActiveBlock returns the style/format Block resolved for the active
// states. This is how clock reads the effective format to derive its tick.
func (c *Ctx) ActiveBlock() Block { return c.host.Block() }

// Refresh schedules an extra redraw. Rarely needed: every hook and source
// delivery is followed by an automatic render.
func (c *Ctx) Refresh() { c.host.Refresh() }

// Go runs fn on a runtime-tracked helper goroutine for blocking one-shot
// work. fn must not touch module state directly.
func (c *Ctx) Go(fn func()) { c.host.Go(fn) }

// HandleVerb binds the implementation of a verb declared in Def.Verbs.
// Call it from Init.
func (c *Ctx) HandleVerb(name string, fn func(VerbArgs) error) {
	c.verbs[name] = fn
}

// Verb looks up a bound verb implementation. Runtime use.
func (c *Ctx) Verb(name string) (func(VerbArgs) error, bool) {
	fn, ok := c.verbs[name]
	return fn, ok
}

func (c *Ctx) addSubscription(s *Subscription) { c.host.SubscriptionAdded(s) }
