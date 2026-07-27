// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"fmt"
	"strings"

	"github.com/nekorg/pawbar/pkg/module"
	"gopkg.in/yaml.v3"
)

// HoverState and PressedState are the built-in user-interaction states,
// applied by the runtime, always last in merge priority.
const (
	HoverState   = "hover"
	PressedState = "pressed"
)

// Table resolves a module instance's style and options for any set of
// active states. It is compiled once per instance (and rebuilt on theme
// changes); resolution itself is cached per active-state combination.
//
// Merge priority, low to high:
//
//	module defaults yaml < theme.defaults < entry base keys
//	  < active states in canonical order, each state's Block being
//	    defaults.states[s] < theme.defaults.states[s] < entry.states[s]
//
// Canonical state order: condition states (Def declaration order), then
// defaults-yaml states, then config-declared user states (yaml order),
// then hover, then pressed.
type Table struct {
	def module.Def

	baseBlock   module.Block
	stateBlocks map[string]module.Block
	stateOrder  []string
	stateRank   map[string]int

	// optionNodes are re-decoded onto a fresh options struct per resolve:
	// yaml decode only touches present keys, which makes it the typed
	// partial-merge operator for module options.
	defsNode       *yaml.Node
	entryNode      *yaml.Node
	stateOptNodes  map[string][]*yaml.Node // low to high priority
	hasStateOption map[string]bool

	blockCache map[string]module.Block
	optsCache  map[string]any
}

// States returns the canonical state order (highest priority last).
func (t *Table) States() []string { return t.stateOrder }

// Known reports whether s is a state this instance knows about.
func (t *Table) Known(s string) bool {
	_, ok := t.stateRank[s]
	return ok
}

// key canonicalizes an active set into a cache key, dropping unknown
// states.
func (t *Table) key(active []string) (string, []string) {
	ordered := make([]string, 0, len(active))
	for _, s := range t.stateOrder {
		if contains(active, s) {
			ordered = append(ordered, s)
		}
	}
	return strings.Join(ordered, "\x00"), ordered
}

// ResolveBlock returns the style/format Block for the active states.
func (t *Table) ResolveBlock(active []string) module.Block {
	k, ordered := t.key(active)
	if b, ok := t.blockCache[k]; ok {
		return b
	}
	b := t.baseBlock
	for _, s := range ordered {
		b = t.stateBlocks[s].Over(b)
	}
	t.blockCache[k] = b
	return b
}

// ResolveOptions returns the module options struct for the active states:
// module defaults, then the entry's option keys, then each active state's
// option keys decoded on top. The result is shared per state combination;
// callers must not mutate it.
func (t *Table) ResolveOptions(active []string) (any, error) {
	if t.def.Options == nil {
		return nil, nil
	}
	k, ordered := t.key(active)
	if o, ok := t.optsCache[k]; ok {
		return o, nil
	}
	opts := t.def.Options()
	if t.defsNode != nil {
		if err := t.defsNode.Decode(opts); err != nil {
			return nil, fmt.Errorf("default options: %s", yamlErr(err))
		}
	}
	if t.entryNode != nil {
		if err := t.entryNode.Decode(opts); err != nil {
			return nil, fmt.Errorf("options: %s", yamlErr(err))
		}
	}
	for _, s := range ordered {
		if !t.hasStateOption[s] {
			continue
		}
		for _, n := range t.stateOptNodes[s] {
			if err := n.Decode(opts); err != nil {
				return nil, fmt.Errorf("state %q options: %s", s, yamlErr(err))
			}
		}
	}
	t.optsCache[k] = opts
	return opts, nil
}

// OptionStates reports whether any state overrides module options (used
// by the runtime to know if a state flip needs an options swap).
func (t *Table) OptionStates() bool {
	for _, has := range t.hasStateOption {
		if has {
			return true
		}
	}
	return false
}

// buildTable compiles the cascade for one entry. defs is the module's
// shipped-defaults mapping (nil when absent or disabled); themeBlock/
// themeStates come from theme.defaults, already decoded.
func buildTable(def module.Def, entry, defs *yaml.Node, theme *compiledTheme,
	path string, issues *Issues,
) *Table {
	t := &Table{
		def:            def,
		stateBlocks:    make(map[string]module.Block),
		stateOptNodes:  make(map[string][]*yaml.Node),
		hasStateOption: make(map[string]bool),
		blockCache:     make(map[string]module.Block),
		optsCache:      make(map[string]any),
		defsNode:       defs,
		entryNode:      entry,
	}

	optTags := optionTags(def)
	entryKeys := append(append(append([]string{}, blockTags...), reservedEntryKeys...), optTags...)

	// Guard against a module options struct shadowing shared keys.
	for _, tag := range optTags {
		if contains(blockTags, tag) || contains(reservedEntryKeys, tag) {
			issues.add(path, entry,
				"module %q options struct illegally re-declares reserved key %q", def.Name, tag)
		}
	}

	// Shipped defaults use the entry schema minus the `defaults` switch.
	dpath := fmt.Sprintf("module %q defaults", def.Name)
	defsKeys := append(append(append([]string{}, blockTags...), "states", "on", "priority"), optTags...)
	checkKeys(defs, dpath, defsKeys, issues)
	defsBlock := decodeBlock(defs, dpath, issues)

	checkKeys(entry, path, entryKeys, issues)
	entryBlock := decodeBlock(entry, path, issues)
	t.baseBlock = entryBlock.Over(theme.block.Over(defsBlock))

	if len(def.Placeholders) > 0 && t.baseBlock.Format == nil && t.baseBlock.Template == nil {
		issues.add(path, entry, "module %q needs a `format` or `template`", def.Name)
	}

	// Every declared option must be set somewhere: the shipped defaults
	// or the entry. With `defaults: false` that makes all of them manual.
	for _, tag := range optTags {
		if subNode(defs, tag) == nil && subNode(entry, tag) == nil {
			issues.add(path, entry, "option %q must be set (no default applies)", tag)
		}
	}

	collectStates := func(n *yaml.Node, path string) (map[string]*yaml.Node, []string) {
		n = subNode(n, "states")
		if isNull(n) {
			return nil, nil
		}
		if n.Kind != yaml.MappingNode {
			issues.add(path+".states", n, "`states` must be a mapping")
			return nil, nil
		}
		states := map[string]*yaml.Node{}
		var order []string
		for k, v := range mappingPairs(n) {
			states[k.Value] = v
			order = append(order, k.Value)
		}
		return states, order
	}
	defsStates, defsStateOrder := collectStates(defs, dpath)
	entryStates, entryStateOrder := collectStates(entry, path)

	// A null entry state (`muted: ~`) clears everything inherited for it.
	removed := map[string]bool{}
	for s, n := range entryStates {
		if isNull(n) {
			removed[s] = true
			delete(entryStates, s)
		}
	}

	// Canonical order: condition states, then shipped/theme/entry user
	// states (declaration order), hover, pressed.
	seen := map[string]bool{}
	push := func(name string) {
		if !seen[name] && name != HoverState && name != PressedState {
			seen[name] = true
			t.stateOrder = append(t.stateOrder, name)
		}
	}
	for _, sd := range def.States {
		push(sd.Name)
	}
	for _, name := range defsStateOrder {
		push(name)
	}
	for _, name := range theme.stateOrder {
		push(name)
	}
	for _, name := range entryStateOrder {
		push(name)
	}
	t.stateOrder = append(t.stateOrder, HoverState, PressedState)

	t.stateRank = make(map[string]int, len(t.stateOrder))
	for i, s := range t.stateOrder {
		t.stateRank[s] = i
	}

	// Per-state Block cascade and option nodes.
	stateKeys := append(append([]string{}, blockTags...), optTags...)
	for _, s := range t.stateOrder {
		var b module.Block
		if removed[s] {
			t.stateBlocks[s] = b
			continue
		}
		if n, ok := defsStates[s]; ok {
			sp := fmt.Sprintf("%s.states.%s", dpath, s)
			checkKeys(n, sp, stateKeys, issues)
			b = decodeBlock(n, sp, issues)
			t.stateOptNodes[s] = append(t.stateOptNodes[s], n)
			t.hasStateOption[s] = t.hasStateOption[s] || nodeHasAnyKey(n, optTags)
		}
		if tb, ok := theme.states[s]; ok {
			b = tb.Over(b)
		}
		if n, ok := entryStates[s]; ok {
			sp := fmt.Sprintf("%s.states.%s", path, s)
			checkKeys(n, sp, stateKeys, issues)
			b = decodeBlock(n, sp, issues).Over(b)
			t.stateOptNodes[s] = append(t.stateOptNodes[s], n)
			t.hasStateOption[s] = t.hasStateOption[s] || nodeHasAnyKey(n, optTags)
		}
		t.stateBlocks[s] = b
	}

	// Surface option-decode type errors at compile time rather than on
	// first state flip.
	if def.Options != nil {
		if _, err := t.ResolveOptions(nil); err != nil {
			issues.add(path, entry, "%v", err)
		}
		for s := range t.hasStateOption {
			if _, err := t.ResolveOptions([]string{s}); err != nil {
				issues.add(fmt.Sprintf("%s.states.%s", path, s), entry, "%v", err)
			}
		}
	}

	return t
}

func nodeHasAnyKey(n *yaml.Node, keys []string) bool {
	for k := range mappingPairs(n) {
		if contains(keys, k.Value) {
			return true
		}
	}
	return false
}

// compiledTheme is theme.defaults decoded once per config load.
type compiledTheme struct {
	block      module.Block
	states     map[string]module.Block
	stateOrder []string
}

func compileTheme(t *Theme, issues *Issues) *compiledTheme {
	ct := &compiledTheme{states: make(map[string]module.Block)}
	if t.Defaults == nil {
		return ct
	}
	allowed := append(append([]string{}, blockTags...), "states")
	checkKeys(t.Defaults, "theme.defaults", allowed, issues)
	ct.block = decodeBlock(t.Defaults, "theme.defaults", issues)

	if sn := subNode(t.Defaults, "states"); sn != nil {
		if sn.Kind != yaml.MappingNode {
			issues.add("theme.defaults.states", sn, "`states` must be a mapping")
			return ct
		}
		for k, v := range mappingPairs(sn) {
			sp := "theme.defaults.states." + k.Value
			checkKeys(v, sp, blockTags, issues)
			ct.states[k.Value] = decodeBlock(v, sp, issues)
			ct.stateOrder = append(ct.stateOrder, k.Value)
		}
	}
	return ct
}
