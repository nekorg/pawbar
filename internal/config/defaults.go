// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"sync"

	"github.com/nekorg/pawbar/pkg/module"
	"gopkg.in/yaml.v3"
)

var defaultsCache sync.Map // module name -> *yaml.Node (nil when none shipped)

// defaultsNode returns the parsed mapping of a module's shipped defaults
// yaml, or nil when the module ships none. The node is parsed once per
// module and shared read-only afterwards; it never goes through expandVars
// because shipped files may only reference built-in `@` color names, not
// user theme vars. module.Register already rejected syntactically invalid
// defaults, so parse failures here cannot happen for registered modules.
func defaultsNode(def module.Def) *yaml.Node {
	if v, ok := defaultsCache.Load(def.Name); ok {
		n, _ := v.(*yaml.Node)
		return n
	}
	var node *yaml.Node
	if len(def.Defaults) > 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(def.Defaults, &doc); err == nil &&
			len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
			node = doc.Content[0]
		}
	}
	defaultsCache.Store(def.Name, node)
	return node
}
