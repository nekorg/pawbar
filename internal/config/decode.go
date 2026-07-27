// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"reflect"
	"strings"
	"sync"

	"github.com/nekorg/pawbar/pkg/module"
	"gopkg.in/yaml.v3"
)

// blockTags are the yaml keys of module.Block, the style surface shared by
// every module.
var blockTags = tagsOf(module.Block{})

// reservedEntryKeys are entry keys that are neither Block style nor module
// options.
var reservedEntryKeys = []string{"states", "on", "defaults", "priority"}

// tagsOf returns the yaml tags of v's struct fields.
func tagsOf(v any) []string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for _, f := range reflect.VisibleFields(t) {
		tag := f.Tag.Get("yaml")
		tag, _, _ = strings.Cut(tag, ",")
		if tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	return out
}

var optionTagsCache sync.Map // module name -> []string

// optionTags returns the yaml keys of a module's options struct, cached
// per module name.
func optionTags(def module.Def) []string {
	if tags, ok := optionTagsCache.Load(def.Name); ok {
		return tags.([]string)
	}
	var tags []string
	if def.Options != nil {
		tags = tagsOf(def.Options())
	}
	optionTagsCache.Store(def.Name, tags)
	return tags
}

// checkKeys walks a mapping node and reports unknown keys against the
// allowed set, with did-you-mean hints.
func checkKeys(n *yaml.Node, path string, allowed []string, issues *Issues) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	set := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		set[k] = true
	}
	for k := range mappingPairs(n) {
		if !set[k.Value] {
			issues.addHint(path, k, didYouMean(k.Value, allowed),
				"unknown key %q", k.Value)
		}
	}
}

// decodeBlock decodes the Block-tagged keys of a mapping into a Block.
// Non-Block keys in the node are ignored (they are options/states/on).
func decodeBlock(n *yaml.Node, path string, issues *Issues) module.Block {
	var b module.Block
	if n == nil {
		return b
	}
	if err := n.Decode(&b); err != nil {
		issues.add(path, n, "%s", yamlErr(err))
	}
	return b
}

// subNode returns the value node of key inside mapping n, or nil.
func subNode(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for k, v := range mappingPairs(n) {
		if k.Value == key {
			return v
		}
	}
	return nil
}
