// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package config parses and validates pawbar's yaml configuration and
// compiles it into per-module instances ready for the runtime.
//
// Nothing here is logged-and-skipped: every problem becomes an Issue with
// a location, and the caller decides between strict failure and rendering
// error chips.
package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Issue is one config problem, positioned in the source file.
type Issue struct {
	Path string // config-tree path, e.g. "right[2].clock.states.hover"
	Line int
	Col  int
	Msg  string
	Hint string // optional "did you mean ...?"
}

func (i Issue) Error() string {
	var b strings.Builder
	if i.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", i.Line, i.Col)
	}
	if i.Path != "" {
		fmt.Fprintf(&b, "%s: ", i.Path)
	}
	b.WriteString(i.Msg)
	if i.Hint != "" {
		fmt.Fprintf(&b, " (%s)", i.Hint)
	}
	return b.String()
}

// Issues collects problems while parsing continues.
type Issues []Issue

// Fatal reports whether the config was unusable as a whole (unreadable
// file, broken yaml, malformed top level) rather than having entry-level
// issues, which compile into error chips. Hot reload keeps the last good
// configuration on fatal issues.
func (is Issues) Fatal() bool {
	for _, i := range is {
		if i.Path == "" {
			return true
		}
	}
	return false
}

func (is *Issues) add(path string, n *yaml.Node, format string, args ...any) {
	issue := Issue{Path: path, Msg: fmt.Sprintf(format, args...)}
	if n != nil {
		issue.Line = n.Line
		issue.Col = n.Column
	}
	*is = append(*is, issue)
}

func (is *Issues) addHint(path string, n *yaml.Node, hint, format string, args ...any) {
	is.add(path, n, format, args...)
	(*is)[len(*is)-1].Hint = hint
}

func (is Issues) Err() error {
	if len(is) == 0 {
		return nil
	}
	msgs := make([]string, len(is))
	for i, issue := range is {
		msgs[i] = issue.Error()
	}
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

// didYouMean returns a hint string when key is within edit distance 2 of a
// known key, else "".
func didYouMean(key string, known []string) string {
	best, bestDist := "", 3
	for _, k := range known {
		if d := levenshtein(key, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("did you mean %q?", best)
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}
