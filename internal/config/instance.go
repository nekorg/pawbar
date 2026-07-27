// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"fmt"

	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
	"gopkg.in/yaml.v3"
)

// Instance is one compiled module slot: everything the runtime needs to
// start (or refuse to start) the module.
type Instance struct {
	Name string
	Def  module.Def
	// Table resolves style/options for any active-state set. Nil when Err
	// is set.
	Table *Table
	// On maps buttons/hover to actions: the shipped defaults' `on:`
	// overridden per-button by the entry's `on:` (a null entry binding
	// removes the button).
	On map[string][]module.Action
	// Priority orders this slot against the others when the bar runs out
	// of room: the lowest steps down its format ladder first. Default 0.
	Priority int
	// Hash identifies the entry's raw configuration; hot reload compares
	// it to decide keep/reconfigure/restart.
	Hash string
	// Err is set when this slot failed compilation; the runtime renders
	// an error chip in its place. The first issue is representative; all
	// are in Bar-level Issues too.
	Err *Issue
}

// Bar is a fully compiled configuration.
type Bar struct {
	Settings BarSettings
	Left     []*Instance
	Middle   []*Instance
	Right    []*Instance
	// GapStyle draws Settings.Gap. Auto-gaps are layout, not modules, so
	// they have no entry of their own to style; they take theme.defaults.
	// Styling one join means writing a `gap` entry there.
	GapStyle vaxis.Style
}

// Instances iterates all slots left to right.
func (b *Bar) Instances() []*Instance {
	out := make([]*Instance, 0, len(b.Left)+len(b.Middle)+len(b.Right))
	out = append(out, b.Left...)
	out = append(out, b.Middle...)
	out = append(out, b.Right...)
	return out
}

// Compile turns a parsed File into runtime instances. Issues accumulate
// across all entries; per-entry failures produce error-chip instances
// rather than dropping the slot.
func Compile(f *File) (*Bar, Issues) {
	var issues Issues
	theme := compileTheme(&f.Theme, &issues)
	barDefaults := f.Bar.Defaults == nil || *f.Bar.Defaults

	bar := &Bar{Settings: f.Bar, GapStyle: theme.block.Style()}
	bar.Left = compileSide(f.Left, "left", theme, barDefaults, &issues)
	bar.Middle = compileSide(f.Middle, "middle", theme, barDefaults, &issues)
	bar.Right = compileSide(f.Right, "right", theme, barDefaults, &issues)
	return bar, issues
}

func compileSide(entries []ModuleEntry, side string, theme *compiledTheme, barDefaults bool, issues *Issues) []*Instance {
	out := make([]*Instance, 0, len(entries))
	for i, e := range entries {
		path := fmt.Sprintf("%s[%d].%s", side, i, e.Name)
		out = append(out, compileEntry(e, path, theme, barDefaults, issues))
	}
	return out
}

func compileEntry(e ModuleEntry, path string, theme *compiledTheme, barDefaults bool, issues *Issues) *Instance {
	inst := &Instance{Name: e.Name, Hash: entryHash(e)}

	def, ok := module.Lookup(e.Name)
	if !ok {
		before := len(*issues)
		hint := didYouMean(e.Name, module.Names())
		if r, gone := removedModules[e.Name]; gone {
			hint = r
		}
		issues.addHint(path, e.node(), hint, "unknown module %q", e.Name)
		inst.Err = &(*issues)[before]
		return inst
	}
	inst.Def = def

	before := len(*issues)

	// The shipped-defaults layer can be dropped bar-wide or per entry.
	useDefaults := barDefaults
	if n := subNode(e.Node, "defaults"); n != nil {
		if err := n.Decode(&useDefaults); err != nil {
			issues.add(path+".defaults", n, "%s", yamlErr(err))
		}
	}
	var defs *yaml.Node
	if useDefaults {
		defs = defaultsNode(def)
	}

	// Shipped defaults may set a priority; the entry overrides it.
	for _, src := range []*yaml.Node{defs, e.Node} {
		n := subNode(src, "priority")
		if n == nil {
			continue
		}
		if err := n.Decode(&inst.Priority); err != nil {
			issues.add(path+".priority", n, "%s", yamlErr(err))
		}
	}

	inst.Table = buildTable(def, e.Node, defs, theme, path, issues)

	inst.On = make(map[string][]module.Action)
	if defs != nil {
		dpath := fmt.Sprintf("module %q defaults", def.Name)
		for btn, acts := range parseOn(subNode(defs, "on"), dpath, issues) {
			if len(acts) > 0 {
				inst.On[btn] = acts
			}
		}
	}
	for btn, acts := range parseOn(subNode(e.Node, "on"), path, issues) {
		if acts == nil { // explicit `button: ~` unbinds the default
			delete(inst.On, btn)
			continue
		}
		inst.On[btn] = acts
	}
	validateActions(inst.On, def, inst.Table.States(), path, e.node(), issues)

	if len(*issues) > before {
		inst.Err = &(*issues)[before]
	}
	return inst
}

// node returns the best node to point issues at: the options node when the
// entry has one, else a positioned stub for bare-name entries.
func (e ModuleEntry) node() *yaml.Node {
	if e.Node != nil {
		return e.Node
	}
	return &yaml.Node{Line: e.Line, Column: e.Col}
}
