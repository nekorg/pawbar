// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"fmt"
	"maps"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is a parsed (but not yet compiled) pawbar.yaml.
type File struct {
	Bar    BarSettings
	Theme  Theme
	Left   []ModuleEntry
	Middle []ModuleEntry
	Right  []ModuleEntry
	// Outputs holds the per-output `outputs:` sections, applied over the
	// rest of the document by For.
	Outputs map[string]*Override

	Path string
	// Output is the monitor this File was specialised for by For, empty
	// for the base document.
	Output string
}

// BarSettings are bar-global options.
type BarSettings struct {
	TruncatePriority []string `yaml:"truncate_priority"`
	EnableEllipsis   *bool    `yaml:"enable_ellipsis"`
	Ellipsis         string   `yaml:"ellipsis"`
	Strict           bool     `yaml:"strict"`
	// Gap is inserted between adjacent non-empty modules on a side.
	// Empty (the default) keeps modules flush, as before; an explicit
	// `gap` entry is never padded with it, so writing one overrides this
	// at that join.
	Gap string `yaml:"gap"`
	// ShrinkMin is the floor, in columns, that an elastic placeholder
	// ({title~}) is never shrunk below.
	ShrinkMin int `yaml:"shrink_min"`
	// Defaults toggles the shipped module-defaults layer bar-wide
	// (default true); entries can override with their own `defaults:`.
	Defaults *bool `yaml:"defaults"`
	// Outputs selects which monitors get a bar (default: all of them).
	Outputs OutputSel `yaml:"outputs"`
	// Kitty tunes the kitty processes the bars and menus draw in.
	Kitty KittySettings `yaml:"kitty"`
}

// KittySettings decide how many kitty processes pawbar runs.
//
// A kitty process costs a couple of hundred megabytes, and with a bar per
// monitor plus pooled menu panels that adds up fast. `host: own` puts every
// panel in one hidden instance instead, where a panel costs a few megabytes.
type KittySettings struct {
	// Host is "own" (one shared instance, the default) or "none" (a kitty
	// process per panel, how pawbar used to work).
	Host string `yaml:"host"`
	// Config is the kitty.conf the shared instance uses: "inherit" for the
	// user's own (the default), "none" for kitty's built-in defaults, or a
	// path. Only used when Host is "own"; panels in a shared instance
	// cannot have their own config.
	Config string `yaml:"config"`
	// Overrides are extra kitty settings (-o) for the shared instance, on
	// top of the ones pawbar needs.
	Overrides []string `yaml:"overrides"`
	// Command is the kitty binary to run.
	Command string `yaml:"command"`
}

// Shared reports whether panels share one kitty process.
func (k KittySettings) Shared() bool { return k.Host == KittyHostOwn }

const (
	KittyHostOwn  = "own"
	KittyHostNone = "none"
)

func (k *KittySettings) fillDefaults() {
	if k.Host == "" {
		k.Host = KittyHostOwn
	}
	if k.Config == "" {
		k.Config = "inherit"
	}
	if k.Command == "" {
		k.Command = "kitty"
	}
}

func (k *KittySettings) validate(n *yaml.Node, issues *Issues) {
	switch k.Host {
	case KittyHostOwn, KittyHostNone:
	default:
		issues.add("bar.kitty.host", n,
			`invalid host %q, valid options are: ["own", "none"]`, k.Host)
	}
}

// ConfigFile is what to pass kitty as --config: empty to inherit the user's
// kitty.conf, "NONE" to ignore it, or a path.
func (k KittySettings) ConfigFile() string {
	switch k.Config {
	case "inherit", "":
		return ""
	case "none":
		return "NONE"
	default:
		return k.Config
	}
}

func (b *BarSettings) fillDefaults() {
	if len(b.TruncatePriority) == 0 {
		b.TruncatePriority = []string{"right", "left", "middle"}
	}
	if b.EnableEllipsis == nil {
		t := true
		b.EnableEllipsis = &t
	}
	if b.Ellipsis == "" {
		b.Ellipsis = "…"
	}
	if b.ShrinkMin == 0 {
		b.ShrinkMin = 3
	}
	b.Outputs.fillDefaults()
	b.Kitty.fillDefaults()
}

func (b *BarSettings) validate(n *yaml.Node, issues *Issues) {
	b.Kitty.validate(subNodeOr(n, "kitty", n), issues)
	b.Outputs.validate(subNodeOr(n, "outputs", n), issues)
	if b.ShrinkMin < 1 {
		issues.add("bar.shrink_min", n, "must be at least 1 column, got %d", b.ShrinkMin)
	}
	if len(b.TruncatePriority) != 3 {
		issues.add("bar.truncate_priority", n,
			"exactly 3 anchors needed, %d provided", len(b.TruncatePriority))
		return
	}
	set := map[string]bool{"left": false, "middle": false, "right": false}
	for _, a := range b.TruncatePriority {
		if _, ok := set[a]; !ok {
			issues.add("bar.truncate_priority", n,
				`invalid anchor %q, valid options are: ["left", "middle", "right"]`, a)
			return
		}
		if set[a] {
			issues.add("bar.truncate_priority", n, "%q listed twice", a)
			return
		}
		set[a] = true
	}
}

// Theme holds user variables and bar-wide defaults.
type Theme struct {
	Vars map[string]string
	// Defaults is the raw `theme.defaults` mapping: Block keys plus an
	// optional `states:` mapping of Blocks. Kept as a node until compile.
	Defaults *yaml.Node
}

// ModuleEntry is one item in a left/middle/right list: a bare name or a
// single-key mapping of name to its options node.
type ModuleEntry struct {
	Name string
	Node *yaml.Node // nil when the entry is a bare name
	Line int
	Col  int
}

var topLevelKeys = []string{"bar", "theme", "left", "middle", "right", "outputs"}

// Load parses config bytes into a File, collecting Issues instead of
// stopping at the first problem. The returned File is usable (with
// defaults) even when issues exist.
func Load(data []byte, path string) (*File, Issues) {
	var issues Issues
	f := &File{Path: path}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		issues.add("", nil, "invalid yaml: %v", err)
		f.Bar.fillDefaults()
		return f, issues
	}
	if len(doc.Content) == 0 { // empty file
		f.Bar.fillDefaults()
		return f, issues
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		issues.add("", root, "top level must be a mapping")
		f.Bar.fillDefaults()
		return f, issues
	}

	var barNode *yaml.Node
	for k, v := range mappingPairs(root) {
		if isNull(v) { // `left:` with nothing under it is an empty section
			continue
		}
		switch k.Value {
		case "bar":
			barNode = v
			if err := v.Decode(&f.Bar); err != nil {
				issues.add("bar", v, "%s", yamlErr(err))
			}
			checkKeys(v, "bar", tagsOf(BarSettings{}), &issues)
		case "theme":
			parseTheme(v, &f.Theme, &issues)
		case "left":
			f.Left = parseEntries(v, "left", &issues)
		case "middle":
			f.Middle = parseEntries(v, "middle", &issues)
		case "right":
			f.Right = parseEntries(v, "right", &issues)
		case "outputs":
			f.Outputs = parseOutputs(v, &issues)
		default:
			issues.addHint("", k, didYouMean(k.Value, topLevelKeys),
				"unknown key %q", k.Value)
		}
	}

	f.Bar.fillDefaults()
	f.Bar.validate(barNode, &issues)

	// Substitute @vars everywhere they can appear. A per-output section
	// sees the base vars plus its own.
	expandFileVars(f, f.Theme.Vars)
	for _, name := range f.OutputNames() {
		o := f.Outputs[name]
		vars := f.Theme.Vars
		if len(o.Theme.Vars) > 0 {
			vars = make(map[string]string, len(f.Theme.Vars)+len(o.Theme.Vars))
			maps.Copy(vars, f.Theme.Vars)
			maps.Copy(vars, o.Theme.Vars)
		}
		if len(vars) > 0 {
			expandVars(o.Theme.Defaults, vars)
			for _, side := range [][]ModuleEntry{o.Left, o.Middle, o.Right} {
				for _, e := range side {
					expandVars(e.Node, vars)
				}
			}
		}
		if !f.Bar.Outputs.Matches(name) {
			issues.add("outputs."+name, o.Key,
				"not selected by bar.outputs (%s), so this section is never used", f.Bar.Outputs)
		}
		// Settings only make sense merged: a base `truncate_priority`
		// plus an override's must still name all three anchors.
		if o.Bar != nil {
			merged := f.For(name)
			merged.Bar.validate(o.Bar, &issues)
		}
	}
	return f, issues
}

// expandFileVars substitutes @vars in the root document's theme defaults
// and module entries.
func expandFileVars(f *File, vars map[string]string) {
	if len(vars) == 0 {
		return
	}
	expandVars(f.Theme.Defaults, vars)
	for _, side := range [][]ModuleEntry{f.Left, f.Middle, f.Right} {
		for _, e := range side {
			expandVars(e.Node, vars)
		}
	}
}

// Read loads and parses the config file at path.
func Read(path string) (*File, Issues) {
	data, err := os.ReadFile(path)
	if err != nil {
		var issues Issues
		issues.add("", nil, "cannot read config: %v", err)
		f := &File{Path: path}
		f.Bar.fillDefaults()
		return f, issues
	}
	return Load(data, path)
}

func parseTheme(n *yaml.Node, t *Theme, issues *Issues) {
	if n.Kind != yaml.MappingNode {
		issues.add("theme", n, "theme must be a mapping")
		return
	}
	for k, v := range mappingPairs(n) {
		if isNull(v) {
			continue
		}
		switch k.Value {
		case "vars":
			if err := v.Decode(&t.Vars); err != nil {
				issues.add("theme.vars", v, "%s", yamlErr(err))
			}
		case "defaults":
			t.Defaults = v
		default:
			issues.addHint("theme", k, didYouMean(k.Value, []string{"vars", "defaults"}),
				"unknown key %q", k.Value)
		}
	}
}

func parseEntries(n *yaml.Node, side string, issues *Issues) []ModuleEntry {
	if n.Kind != yaml.SequenceNode {
		issues.add(side, n, "%s must be a list of modules", side)
		return nil
	}
	out := make([]ModuleEntry, 0, len(n.Content))
	for i, item := range n.Content {
		path := fmt.Sprintf("%s[%d]", side, i)
		switch item.Kind {
		case yaml.ScalarNode:
			out = append(out, ModuleEntry{Name: item.Value, Line: item.Line, Col: item.Column})
		case yaml.MappingNode:
			if len(item.Content) != 2 {
				issues.add(path, item,
					"a module entry must be a bare name or a single `name: {options}` mapping")
				continue
			}
			key, val := item.Content[0], item.Content[1]
			e := ModuleEntry{Name: key.Value, Line: key.Line, Col: key.Column}
			switch {
			case isNull(val): // `- clock:` with nothing under it
			case val.Kind == yaml.ScalarNode:
				e.Node = formatShorthand(val)
			default:
				e.Node = val
			}
			out = append(out, e)
		default:
			issues.add(path, item, "invalid module entry")
		}
	}
	return out
}

// formatShorthand expands a scalar entry value into a one-key options
// mapping: `- gap: " │ "` means `- gap: {format: " │ "}`. Format is the only
// key a scalar entry could plausibly mean, and it keeps a bar's punctuation
// (gaps, dividers) on one line each.
func formatShorthand(v *yaml.Node) *yaml.Node {
	key := &yaml.Node{
		Kind:   yaml.ScalarNode,
		Tag:    "!!str",
		Value:  "format",
		Line:   v.Line,
		Column: v.Column,
	}
	return &yaml.Node{
		Kind:    yaml.MappingNode,
		Tag:     "!!map",
		Content: []*yaml.Node{key, v},
		Line:    v.Line,
		Column:  v.Column,
	}
}

// expandVars replaces scalar values of the form "@name" with the value of
// theme.vars.name, recursively. Unmatched @refs are left for downstream
// parsing (built-in color names like @urgent live there).
func expandVars(n *yaml.Node, vars map[string]string) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode && strings.HasPrefix(n.Value, "@") {
		if v, ok := vars[strings.TrimPrefix(n.Value, "@")]; ok {
			n.Value = v
			n.Tag = "!!str"
		}
		return
	}
	for _, c := range n.Content {
		expandVars(c, vars)
	}
}

// mappingPairs iterates a mapping node's key/value node pairs.
func mappingPairs(n *yaml.Node) func(yield func(*yaml.Node, *yaml.Node) bool) {
	return func(yield func(*yaml.Node, *yaml.Node) bool) {
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i], n.Content[i+1]) {
				return
			}
		}
	}
}

// isNull reports whether a node is yaml null (an empty section).
func isNull(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

// yamlErr strips yaml.v3's "yaml: " prefix so issues read cleanly.
func yamlErr(err error) string {
	return strings.TrimPrefix(err.Error(), "yaml: ")
}
