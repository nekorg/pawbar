// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

// PoolSettings size the pool of pre-warmed menu panels.
type PoolSettings struct {
	// Target is how many spares one monitor wants: one for the menu, one
	// for the submenu that opens behind it.
	Target int
	// Max caps the whole desktop, since a spare costs a process whether
	// anyone opens a menu or not. Negative is automatic, zero turns
	// pooling off and every menu cold starts.
	Max int
}

// plan splits the budget across order, the outputs ranked by intent.
//
// The first one takes its whole chain: a panel is pinned to its monitor when
// kitty creates it, there is one pointer, and only the bar under it can be
// clicked next, so half a chain there serves nobody. What is left hedges the
// other monitors a spare at a time, so every bar's first menu opens warm
// before any bar's second one does.
//
// A negative max is exactly that hedge for every monitor there is.
func plan(order []string, target, max int) map[string]int {
	want := make(map[string]int, len(order))
	if target <= 0 || max == 0 || len(order) == 0 {
		return want
	}
	if max < 0 {
		max = target + len(order) - 1
	}
	budget := min(max, target*len(order))

	n := min(target, budget)
	want[order[0]] = n
	budget -= n

	for level := 1; level <= target && budget > 0; level++ {
		for _, output := range order[1:] {
			if budget == 0 {
				break
			}
			want[output] = level
			budget--
		}
	}
	return want
}
