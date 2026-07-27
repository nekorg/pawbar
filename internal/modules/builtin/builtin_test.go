// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package builtin

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
)

func TestBuiltinsRegistered(t *testing.T) {
	for _, n := range []string{"gap", "clock", "volume", "ws"} {
		if _, ok := module.Lookup(n); !ok {
			t.Errorf("module %q not registered", n)
		}
	}
	// sep and space were folded into gap; an old config must fail loudly
	// rather than find something else under those names.
	for _, n := range []string{"sep", "space"} {
		if _, ok := module.Lookup(n); ok {
			t.Errorf("module %q should have been removed", n)
		}
	}
}

// TestShippedDefaultsCompile compiles every registered module as a bare
// entry, proving each shipped defaults yaml is clean: no unknown keys, bad
// colors, broken formats, or bindings to undeclared verbs/states.
func TestShippedDefaultsCompile(t *testing.T) {
	for _, name := range module.Names() {
		src := fmt.Sprintf("right: [%s]\n", name)
		f, issues := config.Load([]byte(src), name+".yaml")
		bar, ci := config.Compile(f)
		issues = append(issues, ci...)
		if len(issues) > 0 {
			t.Errorf("%s: shipped defaults do not compile: %v", name, issues.Err())
			continue
		}
		if bar.Right[0].Err != nil {
			t.Errorf("%s: instance error: %v", name, bar.Right[0].Err)
		}
	}
}

// TestDocsCoverModules keeps docs/docs/modules.md from drifting behind the
// code: every registered module needs its own section, and that section
// must show its shipped defaults yaml verbatim and mention every option,
// placeholder, state and verb the module declares. Prose (descriptions,
// notes) is still hand-written; this only enforces that nothing derivable
// from the registry is silently missing.
func TestDocsCoverModules(t *testing.T) {
	docBytes, err := os.ReadFile("../../../docs/docs/modules.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docBytes)

	for _, name := range module.Names() {
		def, _ := module.Lookup(name)
		sec, ok := docSection(doc, name)
		if !ok {
			t.Errorf("%s: no section in docs/docs/modules.md (add a heading mentioning `%s`)", name, name)
			continue
		}
		miss := func(kind, text string) {
			if !strings.Contains(sec, text) {
				t.Errorf("%s: %s %q not documented in its section", name, kind, text)
			}
		}
		if len(def.Defaults) > 0 {
			miss("shipped defaults yaml", strings.TrimSpace(string(def.Defaults)))
		}
		// A section may delegate its option/placeholder/state/verb tables
		// to another module ("identical to [`disk`]"); a link to another
		// section (`](#`) opts out of the per-item checks.
		if strings.Contains(sec, "](#") {
			continue
		}
		for _, key := range optionKeys(def) {
			miss("option", "`"+key+"`")
		}
		for _, ph := range def.Placeholders {
			miss("placeholder", "{"+ph.Name+"}")
		}
		for _, st := range def.States {
			miss("state", "`"+st.Name+"`")
		}
		for _, vb := range def.Verbs {
			miss("verb", "`"+vb.Name+"`")
		}
	}
}

// docSection returns the body under the module heading that mentions
// `name`, up to the next "## " heading. Headings may cover more than one
// module (e.g. "## `cpu`, `ram`"), so it matches on the backticked name
// rather than the whole heading.
func docSection(doc, name string) (string, bool) {
	token := "`" + name + "`"
	lines := strings.Split(doc, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "## ") || !strings.Contains(ln, token) {
			continue
		}
		var b strings.Builder
		for _, l := range lines[i+1:] {
			if strings.HasPrefix(l, "## ") {
				break
			}
			b.WriteString(l)
			b.WriteByte('\n')
		}
		return b.String(), true
	}
	return "", false
}

// optionKeys reflects a module's option struct for its yaml keys.
func optionKeys(def module.Def) []string {
	if def.Options == nil {
		return nil
	}
	v := reflect.ValueOf(def.Options())
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	var keys []string
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// The shipped example config must compile without a single issue against
// the real module definitions.
func TestExampleConfigCompiles(t *testing.T) {
	src, err := os.ReadFile("../../../docs/examples/pawbar.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f, issues := config.Load(src, "pawbar.yaml")
	bar, ci := config.Compile(f)
	issues = append(issues, ci...)
	if len(issues) > 0 {
		t.Fatalf("example config has issues: %v", issues.Err())
	}
	for _, inst := range bar.Instances() {
		if inst.Err != nil {
			t.Errorf("instance %s: %v", inst.Name, inst.Err)
		}
	}
}
