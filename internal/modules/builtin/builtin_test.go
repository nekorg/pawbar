// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package builtin

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
)

func TestBuiltinsRegistered(t *testing.T) {
	for _, n := range []string{"sep", "space", "clock", "volume", "ws"} {
		if _, ok := module.Lookup(n); !ok {
			t.Errorf("module %q not registered", n)
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

// TestDocsShowShippedDefaults keeps docs/docs/modules.md from drifting:
// every module's shipped defaults yaml must appear verbatim in the page.
func TestDocsShowShippedDefaults(t *testing.T) {
	docBytes, err := os.ReadFile("../../../docs/docs/modules.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docBytes)
	for _, name := range module.Names() {
		def, _ := module.Lookup(name)
		if len(def.Defaults) == 0 {
			continue
		}
		if !strings.Contains(doc, strings.TrimSpace(string(def.Defaults))) {
			t.Errorf("%s: shipped defaults yaml not shown verbatim in docs/docs/modules.md", name)
		}
	}
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
