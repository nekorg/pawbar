// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// OutputSel is `bar.outputs`: which monitors get a bar. Unset means every
// connected output; `all` and `none` are spelled out, and anything else is
// a name (or list of names) matched against the compositor's output names.
type OutputSel struct {
	All   bool
	Names []string
	set   bool
}

// Matches reports whether the output named name should get a bar.
func (s OutputSel) Matches(name string) bool {
	if s.All {
		return true
	}
	return slices.Contains(s.Names, name)
}

// Empty reports whether the selection can never match, i.e. `outputs: none`.
func (s OutputSel) Empty() bool { return !s.All && len(s.Names) == 0 }

func (s OutputSel) String() string {
	if s.All {
		return "all"
	}
	if len(s.Names) == 0 {
		return "none"
	}
	return "[" + strings.Join(s.Names, ", ") + "]"
}

func (s *OutputSel) UnmarshalYAML(n *yaml.Node) error {
	s.set = true
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Value {
		case "all":
			s.All = true
		case "none", "":
			// no outputs
		default:
			s.Names = []string{n.Value}
		}
		return nil
	case yaml.SequenceNode:
		var names []string
		if err := n.Decode(&names); err != nil {
			return err
		}
		if slices.Contains(names, "all") {
			s.All = true
			return nil
		}
		s.Names = names
		return nil
	default:
		return &yaml.TypeError{Errors: []string{"outputs must be `all`, `none`, an output name or a list of names"}}
	}
}

func (s *OutputSel) fillDefaults() {
	if !s.set {
		s.All = true
	}
}

func (s OutputSel) validate(n *yaml.Node, issues *Issues) {
	seen := make(map[string]bool, len(s.Names))
	for _, name := range s.Names {
		switch {
		case name == "":
			issues.add("bar.outputs", n, "output name cannot be empty")
		case seen[name]:
			issues.add("bar.outputs", n, "output %q listed twice", name)
		}
		seen[name] = true
	}
}

// Override is one `outputs:` section: the same shape as the root document,
// applied over it for the bar running on that output.
type Override struct {
	Bar   *yaml.Node
	Theme Theme

	Left   []ModuleEntry
	Middle []ModuleEntry
	Right  []ModuleEntry
	// A side is only replaced when the section mentions it; mentioning it
	// with nothing under it empties that side on this output.
	hasLeft, hasMiddle, hasRight bool

	Key *yaml.Node // the output-name key, for issue positions
}

var outputKeys = []string{"bar", "theme", "left", "middle", "right"}

// parseOutputs parses the top-level `outputs:` mapping of output name to
// overrides.
func parseOutputs(n *yaml.Node, issues *Issues) map[string]*Override {
	if n.Kind != yaml.MappingNode {
		issues.add("outputs", n, "outputs must be a mapping of output name to overrides")
		return nil
	}
	out := make(map[string]*Override)
	for k, v := range mappingPairs(n) {
		name := k.Value
		path := "outputs." + name
		if _, dup := out[name]; dup {
			issues.add(path, k, "output %q listed twice", name)
			continue
		}
		o := &Override{Key: k}
		out[name] = o

		if isNull(v) { // `outputs: {eDP-1:}` overrides nothing
			continue
		}
		if v.Kind != yaml.MappingNode {
			issues.add(path, v, "must be a mapping of bar/theme/left/middle/right")
			continue
		}
		for ok, ov := range mappingPairs(v) {
			opath := path + "." + ok.Value
			switch ok.Value {
			case "bar":
				if isNull(ov) {
					continue
				}
				o.Bar = ov
				checkKeys(ov, opath, tagsOf(BarSettings{}), issues)
				if sel := subNode(ov, "outputs"); sel != nil {
					issues.add(opath+".outputs", sel,
						"bar.outputs selects which monitors get a bar; it has no effect inside an outputs: section")
				}
				// Decode once to surface type errors here rather than
				// silently at merge time.
				bar := BarSettings{}
				if err := ov.Decode(&bar); err != nil {
					issues.add(opath, ov, "%s", yamlErr(err))
				}
			case "theme":
				if isNull(ov) {
					continue
				}
				parseTheme(ov, &o.Theme, issues)
			case "left", "middle", "right":
				entries := []ModuleEntry(nil)
				if !isNull(ov) {
					entries = parseEntries(ov, opath, issues)
				}
				switch ok.Value {
				case "left":
					o.Left, o.hasLeft = entries, true
				case "middle":
					o.Middle, o.hasMiddle = entries, true
				case "right":
					o.Right, o.hasRight = entries, true
				}
			default:
				issues.addHint(path, ok, didYouMean(ok.Value, outputKeys),
					"unknown key %q", ok.Value)
			}
		}
	}
	return out
}

// For returns the configuration for the bar running on output, with that
// output's `outputs:` section applied. Sides are replaced wholesale (lists
// cannot be merged predictably); bar and theme merge key by key.
func (f *File) For(output string) *File {
	if f == nil {
		return nil
	}
	o := f.Outputs[output]
	if o == nil {
		return f
	}

	c := *f
	c.Output = output

	if o.Bar != nil {
		bar := f.Bar
		// Decoding into a populated struct leaves absent keys alone, so
		// this is a per-key override of the base settings.
		_ = o.Bar.Decode(&bar)
		bar.fillDefaults()
		c.Bar = bar
	}

	if len(o.Theme.Vars) > 0 {
		vars := make(map[string]string, len(f.Theme.Vars)+len(o.Theme.Vars))
		maps.Copy(vars, f.Theme.Vars)
		maps.Copy(vars, o.Theme.Vars)
		c.Theme.Vars = vars
	}
	if o.Theme.Defaults != nil {
		c.Theme.Defaults = mergeNodes(f.Theme.Defaults, o.Theme.Defaults)
	}

	if o.hasLeft {
		c.Left = o.Left
	}
	if o.hasMiddle {
		c.Middle = o.Middle
	}
	if o.hasRight {
		c.Right = o.Right
	}
	return &c
}

// Outputs returns the output names that have an `outputs:` section, sorted.
func (f *File) OutputNames() []string {
	names := make([]string, 0, len(f.Outputs))
	for name := range f.Outputs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// mergeNodes returns a new mapping node with over's keys applied over
// base's: a key present in both replaces the base value, except when both
// sides are mappings, where the merge recurses (so `states:` merges per
// state rather than replacing the lot). Neither input is mutated.
func mergeNodes(base, over *yaml.Node) *yaml.Node {
	if over == nil {
		return base
	}
	if base == nil || base.Kind != yaml.MappingNode || over.Kind != yaml.MappingNode {
		return over
	}

	merged := *base
	merged.Content = slices.Clone(base.Content)

	for ok, ov := range mappingPairs(over) {
		replaced := false
		for i := 0; i+1 < len(merged.Content); i += 2 {
			if merged.Content[i].Value != ok.Value {
				continue
			}
			merged.Content[i+1] = mergeNodes(merged.Content[i+1], ov)
			replaced = true
			break
		}
		if !replaced {
			merged.Content = append(merged.Content, ok, ov)
		}
	}
	return &merged
}
