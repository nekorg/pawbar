// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"fmt"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// Module is the minimal contract a pawbar module implements. Everything
// else (mouse input, hover, reconfiguration, teardown) is opt-in via the
// optional interfaces below. All methods run serially on one goroutine
// owned by the runtime.
type Module interface {
	// Init prepares the module: subscribe to sources with On, bind verb
	// implementations with ctx.HandleVerb, read ctx.Options(). Returning
	// an error puts the module into the error-chip state.
	Init(ctx *Ctx) error
	// Render emits the module's current segments. It is called by the
	// runtime after every delivered event/hook; it should read module
	// state and write to w without side effects.
	Render(w *Writer)
}

// MouseHandler receives raw mouse events (after config `on:` bindings run).
// Most modules don't need it: verbs and state bindings cover the common
// cases; implement it for per-region behavior beyond a verb.
type MouseHandler interface {
	OnMouse(ctx *Ctx, ev Mouse)
}

// HoverObserver is notified when the pointer enters or leaves the module.
// Styling on hover needs no code (the built-in "hover" state); implement
// this only for behavioral reactions.
type HoverObserver interface {
	OnHover(ctx *Ctx, entered bool, region string)
}

// StateObserver is notified after the active state set (and therefore the
// resolved options) changed. Clock uses this to retune its ticker to the
// new format.
type StateObserver interface {
	OnState(ctx *Ctx)
}

// Reconfigurer opts into in-place reconfiguration on hot reload: the
// runtime swaps ctx.Options() and calls OnConfig instead of restarting the
// instance. Returning an error falls back to a restart.
type Reconfigurer interface {
	OnConfig(ctx *Ctx) error
}

// Stopper releases resources at teardown (hot-reload removal or shutdown).
// Subscriptions are already closed when Stop runs.
type Stopper interface {
	Stop(ctx *Ctx)
}

// Mouse is a data-only mouse event.
type Mouse struct {
	Button string // "left", "right", "middle", "scroll-up", "scroll-down", "scroll-left", "scroll-right"
	Kind   string // "press", "release", "motion"
	Region string // Region of the segment under the pointer, if any
	Col    int    // bar column of the pointer
	XPixel int
	YPixel int
}

// Buttons that a config `on:` mapping (and Mouse.Button) may use, plus the
// pseudo-button "hover".
var Buttons = []string{
	"left", "right", "middle",
	"scroll-up", "scroll-down", "scroll-left", "scroll-right",
}

// Action is one bound reaction to a mouse button or hover. Exactly one
// field group is used per action; config parsing validates that.
type Action struct {
	Verb   string   // invoke a module verb, with Args
	Args   []string //
	Run    []string // spawn a command (argv)
	Notify string   // notify-send a message
	Set    string   // toggle a user-defined state
	Cycle  []string // cycle through user-defined states (off -> [0] -> [1] -> ... -> off)
}

// StateDef declares a condition state the module switches with
// ctx.SetState ("muted", "charging"). Declaration order is the states'
// merge priority; default styling lives in the module's Defaults yaml
// under `states:`.
type StateDef struct {
	Name string
	Doc  string
}

// Kind describes a placeholder's value type, for documentation and spec
// validation.
type Kind int

const (
	KindString Kind = iota
	KindNumber
	KindTime
)

// Placeholder documents one value a module exposes to format strings.
type Placeholder struct {
	Name string
	Doc  string
	Kind Kind
}

// VerbDef declares a named action a module implements ("toggle-mute",
// "goto"). Config references verbs by name; unknown names are config
// errors.
type VerbDef struct {
	Name string
	Doc  string
}

// VerbArgs carries the invocation context of a verb.
type VerbArgs struct {
	Args   []string // extra words from the config binding
	Region string   // region under the pointer when mouse-invoked
	XPixel int      // pointer position when mouse-invoked, 0 otherwise
	YPixel int
}

// Def is a module's complete registration: identity, constructor, and the
// static metadata (options, states, placeholders, verbs, defaults) that
// config validation and documentation are built from.
type Def struct {
	Name string
	Doc  string
	// New returns a fresh, un-initialized module instance.
	New func() Module
	// Options returns a pointer to a zero value of the module's options
	// struct (yaml-tagged). Nil for modules without options.
	Options func() any
	// States lists condition states the module sets; declaration order is
	// their merge priority.
	States []StateDef
	// Placeholders lists the format-string values Render provides.
	Placeholders []Placeholder
	// Verbs lists the named actions the module handles.
	Verbs []VerbDef
	// Defaults is the module's shipped default configuration: a yaml
	// mapping in exactly the user entry schema (style keys, option keys,
	// `states:`, `on:`), usually a go:embed-ed <name>.yaml. It is the
	// bottom layer of the config cascade; users can override any key or
	// drop the whole layer with `defaults: false`.
	Defaults []byte
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Def)
)

// Register adds a module definition; it panics on duplicate or anonymous
// registrations (registration happens in init functions).
func Register(def Def) {
	if def.Name == "" {
		panic("module.Register: empty name")
	}
	if def.New == nil {
		panic(fmt.Sprintf("module.Register(%q): nil New", def.Name))
	}
	if len(def.Defaults) > 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(def.Defaults, &doc); err != nil {
			panic(fmt.Sprintf("module.Register(%q): invalid defaults yaml: %v", def.Name, err))
		}
		if len(doc.Content) > 0 && doc.Content[0].Kind != yaml.MappingNode {
			panic(fmt.Sprintf("module.Register(%q): defaults yaml must be a mapping", def.Name))
		}
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[def.Name]; dup {
		panic(fmt.Sprintf("module.Register(%q): already registered", def.Name))
	}
	registry[def.Name] = def
}

// Lookup returns the registered definition for name.
func Lookup(name string) (Def, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[name]
	return d, ok
}

// Names returns all registered module names, sorted.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
