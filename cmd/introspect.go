// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"gopkg.in/yaml.v3"
)

// printDefaults implements `pawbar defaults [module...]`: with no names it
// lists every registered module, otherwise it prints each module's shipped
// defaults yaml verbatim (the file IS the source of truth).
func printDefaults(names []string) int {
	if len(names) == 0 {
		for _, name := range module.Names() {
			def, _ := module.Lookup(name)
			fmt.Printf("%-16s %s\n", name, def.Doc)
		}
		return 0
	}
	code := 0
	for _, name := range names {
		def, ok := module.Lookup(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown module %q\n", name)
			code = 1
			continue
		}
		if len(names) > 1 {
			fmt.Printf("# %s\n", name)
		}
		if len(def.Defaults) == 0 {
			fmt.Printf("# %s ships no defaults\n", name)
			continue
		}
		os.Stdout.Write(def.Defaults)
	}
	return code
}

// dumpResolved implements `pawbar --resolved`: the user's config compiled
// and flattened per slot, for debugging the cascade. States and hover are
// not expanded; this shows the base layer each module starts from. With
// --output it resolves the bar as that monitor sees it.
func dumpResolved(output string) int {
	f, issues := config.Read(configPath())
	if output != "" && f.Outputs[output] == nil && len(f.Outputs) > 0 {
		fmt.Fprintf(os.Stderr, "# %s has no outputs: section; showing the base configuration\n", output)
	}
	bar, ci := config.Compile(f.For(output))
	issues = append(issues, ci...)
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "%s\n", issue.Error())
	}

	sides := []struct {
		name  string
		insts []*config.Instance
	}{{"left", bar.Left}, {"middle", bar.Middle}, {"right", bar.Right}}

	for _, s := range sides {
		for i, inst := range s.insts {
			fmt.Printf("# %s[%d] %s\n", s.name, i, inst.Name)
			if inst.Err != nil {
				fmt.Printf("# broken: %s\n\n", inst.Err.Error())
				continue
			}
			out := map[string]any{}
			if opts, err := inst.Table.ResolveOptions(nil); err == nil && opts != nil {
				if b, err := yaml.Marshal(opts); err == nil {
					_ = yaml.Unmarshal(b, &out)
				}
			}
			blockInto(out, inst.Table.ResolveBlock(nil))
			if inst.Priority != 0 {
				out["priority"] = inst.Priority
			}
			if on := onInto(inst.On); len(on) > 0 {
				out["on"] = on
			}
			b, err := yaml.Marshal(out)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", inst.Name, err)
				continue
			}
			os.Stdout.Write(b)
			fmt.Println()
		}
	}
	if issues.Fatal() {
		return 1
	}
	return 0
}

func blockInto(out map[string]any, b module.Block) {
	if b.Fg != nil {
		out["fg"] = *b.Fg
	}
	if b.Bg != nil {
		out["bg"] = *b.Bg
	}
	flags := map[string]*bool{
		"bold": b.Bold, "dim": b.Dim, "italic": b.Italic,
		"underline": b.Underline, "blink": b.Blink, "reverse": b.Reverse,
		"strikethrough": b.Strikethrough,
	}
	for k, v := range flags {
		if v != nil {
			out[k] = *v
		}
	}
	if b.Cursor != nil {
		out["cursor"] = string(*b.Cursor)
	}
	// MarshalYAML rather than String, so a format ladder round-trips as the
	// yaml list it was written as.
	if b.Format != nil {
		out["format"], _ = b.Format.MarshalYAML()
	}
	if b.Template != nil {
		out["template"], _ = b.Template.MarshalYAML()
	}
}

func onInto(on map[string][]module.Action) map[string]any {
	out := map[string]any{}
	for btn, acts := range on {
		vals := make([]any, 0, len(acts))
		for _, a := range acts {
			vals = append(vals, actionValue(a))
		}
		if len(vals) == 1 {
			out[btn] = vals[0]
		} else {
			out[btn] = vals
		}
	}
	return out
}

func actionValue(a module.Action) any {
	switch {
	case a.Verb != "":
		if len(a.Args) > 0 {
			return a.Verb + " " + strings.Join(a.Args, " ")
		}
		return a.Verb
	case len(a.Run) > 0:
		return map[string]any{"run": strings.Join(a.Run, " ")}
	case a.Notify != "":
		return map[string]any{"notify": a.Notify}
	case a.Set != "":
		return map[string]any{"set": a.Set}
	default:
		return map[string]any{"cycle": a.Cycle}
	}
}
