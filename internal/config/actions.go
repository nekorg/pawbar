// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nekorg/pawbar/pkg/module"
	"gopkg.in/yaml.v3"
)

// onKeys are the buttons an `on:` mapping may bind, plus hover.
var onKeys = append(append([]string{}, module.Buttons...), "hover")

// parseOn parses an entry's `on:` mapping into per-button action lists.
//
// Grammar per binding:
//
//	left: toggle-mute              # verb (words after the name are args)
//	left: { run: "cmd args" }      # or run: [cmd, arg]
//	left: { notify: "hello" }
//	left: { set: compact }         # toggle a state
//	left: { cycle: [compact, full] }
//	left: [toggle-mute, {notify: done}]   # several actions
func parseOn(n *yaml.Node, path string, issues *Issues) map[string][]module.Action {
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		issues.add(path, n, "`on` must be a mapping of button to action")
		return nil
	}
	out := make(map[string][]module.Action)
	for k, v := range mappingPairs(n) {
		btn := k.Value
		if !contains(onKeys, btn) {
			hint := didYouMean(btn, onKeys)
			// pawbar's old config called scroll buttons wheel-*.
			if scroll := strings.Replace(btn, "wheel-", "scroll-", 1); scroll != btn && contains(onKeys, scroll) {
				hint = fmt.Sprintf("did you mean %q?", scroll)
			}
			issues.addHint(path, k, hint, "unknown button %q", btn)
			continue
		}
		if isNull(v) { // `left: ~` explicitly unbinds an inherited default
			out[btn] = nil
			continue
		}
		acts := parseActions(v, path+"."+btn, issues)
		if len(acts) > 0 {
			out[btn] = acts
		}
	}
	return out
}

func parseActions(n *yaml.Node, path string, issues *Issues) []module.Action {
	switch n.Kind {
	case yaml.SequenceNode:
		var out []module.Action
		for _, item := range n.Content {
			if a, ok := parseAction(item, path, issues); ok {
				out = append(out, a)
			}
		}
		return out
	default:
		if a, ok := parseAction(n, path, issues); ok {
			return []module.Action{a}
		}
		return nil
	}
}

var actionKeys = []string{"verb", "args", "run", "notify", "set", "cycle"}

func parseAction(n *yaml.Node, path string, issues *Issues) (module.Action, bool) {
	var a module.Action
	switch n.Kind {
	case yaml.ScalarNode:
		words := strings.Fields(n.Value)
		if len(words) == 0 {
			issues.add(path, n, "empty action")
			return a, false
		}
		a.Verb = words[0]
		a.Args = words[1:]
		return a, true

	case yaml.MappingNode:
		checkKeys(n, path, actionKeys, issues)
		primaries := 0
		for k, v := range mappingPairs(n) {
			switch k.Value {
			case "verb":
				if err := v.Decode(&a.Verb); err != nil {
					issues.add(path, v, "bad verb: %s", yamlErr(err))
				}
				primaries++
			case "args":
				if err := v.Decode(&a.Args); err != nil {
					issues.add(path, v, "bad args: %s", yamlErr(err))
				}
			case "run":
				if v.Kind == yaml.SequenceNode {
					if err := v.Decode(&a.Run); err != nil {
						issues.add(path, v, "bad run: %s", yamlErr(err))
					}
				} else {
					var s string
					if err := v.Decode(&s); err != nil {
						issues.add(path, v, "bad run: %s", yamlErr(err))
					}
					a.Run = strings.Fields(s)
				}
				primaries++
			case "notify":
				if err := v.Decode(&a.Notify); err != nil {
					issues.add(path, v, "bad notify: %s", yamlErr(err))
				}
				primaries++
			case "set":
				if err := v.Decode(&a.Set); err != nil {
					issues.add(path, v, "bad set: %s", yamlErr(err))
				}
				primaries++
			case "cycle":
				if err := v.Decode(&a.Cycle); err != nil {
					issues.add(path, v, "bad cycle: %s", yamlErr(err))
				}
				primaries++
			}
		}
		if primaries != 1 {
			issues.add(path, n,
				"an action needs exactly one of verb/run/notify/set/cycle, got %d", primaries)
			return a, false
		}
		return a, true

	default:
		issues.add(path, n, "an action must be a string or a mapping")
		return a, false
	}
}

// validateActions checks verb and state references of a compiled binding
// map against the module's definition and the instance's known states.
func validateActions(on map[string][]module.Action, def module.Def,
	knownStates []string, path string, entry *yaml.Node, issues *Issues,
) {
	verbNames := make([]string, len(def.Verbs))
	for i, v := range def.Verbs {
		verbNames[i] = v.Name
	}
	for btn, acts := range on {
		p := path + ".on." + btn
		for _, a := range acts {
			switch {
			case a.Verb != "":
				if !contains(verbNames, a.Verb) {
					issues.addHint(p, entry, didYouMean(a.Verb, verbNames),
						"module %q has no verb %q", def.Name, a.Verb)
				}
			case a.Set != "":
				if !contains(knownStates, a.Set) {
					issues.addHint(p, entry, didYouMean(a.Set, knownStates),
						"unknown state %q", a.Set)
				}
			case len(a.Cycle) > 0:
				for _, s := range a.Cycle {
					if !contains(knownStates, s) {
						issues.addHint(p, entry, didYouMean(s, knownStates),
							"unknown state %q", s)
					}
				}
			}
		}
	}
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }
