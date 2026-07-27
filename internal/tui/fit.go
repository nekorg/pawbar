// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

// run is one laid-out segment: its cells, plus the flex weight that says
// how readily it gives up columns. shrink 0 is rigid — icons, separators,
// the literal text around a placeholder.
type run struct {
	cells  []cell
	shrink int
}

// shrink fits every side into the room its anchor actually has, by
// shortening elastic runs in place, and reports whether they all fit.
//
// The room is not simply the bar width shared three ways. Render places the
// sides in bar.truncate_priority order, and a middle block is *centered*:
// once the clock sits in the middle, the right side can only use the columns
// past it, however empty the left half is. Counting columns bar-wide instead
// would let a side look like it fits and then be positionally trimmed
// anyway — head-first, eating exactly the icon the elastic markers exist to
// protect.
func shrink(runs *[3][]run) bool {
	// lo/hi are the free columns left by the sides placed so far; mlo/mhi
	// are where a centered middle block landed, once there is one.
	lo, hi := 0, width
	mlo, mhi := -1, -1

	fits := true
	for _, side := range drawOrder() {
		if !shrinkSide(runs[side], max(budgetOf(side, lo, hi, mlo, mhi), 0)) {
			fits = false
		}
		w := runsWidth(runs[side])
		switch anchor(side) {
		case left:
			lo += w
		case right:
			hi -= w
		case middle:
			if w > 0 {
				mlo = (width - w) / 2
				mhi = mlo + w
			}
		}
	}
	return fits
}

// budgetOf is how many columns a side can still draw into, mirroring how
// Render measures the free space for that anchor.
func budgetOf(side, lo, hi, mlo, mhi int) int {
	switch anchor(side) {
	case left:
		if mlo >= 0 {
			return mlo - lo
		}
		return hi - lo
	case right:
		if mhi >= 0 {
			return hi - mhi
		}
		return hi - lo
	default: // middle: centered, so it grows symmetrically from the center
		return min(width-2*lo, 2*hi-width)
	}
}

// drawOrder is the order Render places the sides in — bar.truncate_priority,
// earliest first. Whatever is placed first keeps its room; the sides after
// it fit around what is left.
func drawOrder() [3]int {
	out := [3]int{0, 1, 2}
	if len(truncOrder) != len(out) {
		return out
	}
	for i, name := range truncOrder {
		out[i] = int(anchorOf(name))
	}
	return out
}

// shrinkSide fits one side into budget columns and reports whether it made
// it. The deficit is split by weighted max-min fairness: the widest elastic
// run gives way first, and once runs are level they shrink together. Nothing
// drops below shrinkMin columns and nothing rigid is touched — a play/pause
// icon does not disappear so that a song title can stay long.
func shrinkSide(runs []run, budget int) bool {
	total := 0
	var elastic []*run
	for i := range runs {
		total += totalWidth(runs[i].cells)
		if runs[i].shrink > 0 {
			elastic = append(elastic, &runs[i])
		}
	}
	if total <= budget {
		return true
	}
	if len(elastic) == 0 {
		return false
	}

	naturals := make([]int, len(elastic))
	floors := make([]int, len(elastic))
	weights := make([]int, len(elastic))
	elasticTotal := 0
	for i, r := range elastic {
		naturals[i] = totalWidth(r.cells)
		floors[i] = min(naturals[i], shrinkMin)
		weights[i] = r.shrink
		elasticTotal += naturals[i]
	}

	alloc := waterfill(naturals, floors, weights, elasticTotal-(total-budget))
	fitted := 0
	for i, r := range elastic {
		if alloc[i] < naturals[i] {
			r.cells = trimStart(r.cells, alloc[i], useEllipsis)
		}
		fitted += alloc[i]
	}
	return total-elasticTotal+fitted <= budget
}

// runsWidth measures a laid-out side in bar columns.
func runsWidth(runs []run) int {
	w := 0
	for _, r := range runs {
		w += totalWidth(r.cells)
	}
	return w
}

// fit lays every side out at the widest detail level that fits.
//
// Shrinking is tried first, because it only ever shortens text. Only when
// the elastic runs have hit their floor does a module step down its format
// ladder, which is the one thing allowed to drop structure — and the lowest
// priority goes first. If the ladders run out too, Render's positional trim
// is still there as the last resort.
//
// Levels are recomputed from scratch every frame, so widening the bar back
// out restores the detail a narrower one gave up.
func fit() [3][]run {
	for side := range levels {
		clear(levels[side])
	}
	runs := [3][]run{flatten(0), flatten(1), flatten(2)}
	for !shrink(&runs) && stepDown() {
		runs = [3][]run{flatten(0), flatten(1), flatten(2)}
	}
	return runs
}

// stepDown moves one slot to its next, more compact level and reports
// whether it found one. Candidates are ordered by priority, then by
// distance from the bar's outer edge (the innermost gives way first), then
// by the side's own truncation priority — enough to make the choice
// deterministic without asking the user to rank every module.
func stepDown() bool {
	best, bestSide := -1, -1
	var bestRank rank
	for side := range snapshots {
		for idx := range snapshots[side] {
			if levels[side][idx]+1 >= len(snapshots[side][idx]) {
				continue
			}
			r := rank{slotPriority(side, idx), edgeDistance(side, idx), sideRank(side)}
			if best == -1 || r.less(bestRank) {
				best, bestSide, bestRank = idx, side, r
			}
		}
	}
	if best == -1 {
		return false
	}
	levels[bestSide][best]++
	return true
}

// rank orders step-down candidates; lower goes first.
type rank [3]int

func (r rank) less(o rank) bool {
	for i := range r {
		if r[i] != o[i] {
			return r[i] < o[i]
		}
	}
	return false
}

func slotPriority(side, idx int) int {
	if idx < len(priorities[side]) {
		return priorities[side][idx]
	}
	return 0
}

// edgeDistance is how far a slot sits from the outer edge its side is
// anchored to. Negated so that the innermost slot sorts first.
func edgeDistance(side, idx int) int {
	if side == 2 { // right: the outer edge is the end of the list
		return idx - len(snapshots[side])
	}
	return -idx
}

// sideRank orders the sides by bar.truncate_priority: the side listed first
// keeps its content longest, so it degrades last.
func sideRank(side int) int {
	name := [3]string{"left", "middle", "right"}[side]
	for i, n := range truncOrder {
		if n == name {
			return len(truncOrder) - i
		}
	}
	return 0
}

// waterfill distributes budget columns over the elastic runs by weighted
// max-min fairness: find the highest level lambda at which every run takes
// lambda*weight columns — capped at what it actually wants, floored at what
// it must keep — then spend the columns integer rounding left over.
//
// The result is what "shrink the longer one until they match, then shrink
// both together" means precisely, and it is a function of the widths alone,
// so the same bar width always lays out the same way.
func waterfill(naturals, floors, weights []int, budget int) []int {
	take := func(lambda int) ([]int, int) {
		out := make([]int, len(naturals))
		sum := 0
		for i := range naturals {
			out[i] = min(max(lambda*weights[i], floors[i]), naturals[i])
			sum += out[i]
		}
		return out, sum
	}

	// A run never gets more than the whole bar, and weights are >= 1, so
	// width bounds lambda.
	lo, hi := 0, width
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if _, sum := take(mid); sum <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	alloc, sum := take(lo)

	// Hand the remainder to whoever is furthest from its natural width.
	// Ties go to the lower index: an arbitrary but *stable* choice, so the
	// layout doesn't flicker between two equally fair answers.
	for sum < budget {
		best := -1
		for i := range alloc {
			if alloc[i] >= naturals[i] {
				continue
			}
			if best == -1 || naturals[i]-alloc[i] > naturals[best]-alloc[best] {
				best = i
			}
		}
		if best == -1 {
			break
		}
		alloc[best]++
		sum++
	}
	return alloc
}
