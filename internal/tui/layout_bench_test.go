// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tui

import (
	"fmt"
	"testing"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

// segs builds a one-level ladder out of alternating icon/text segments, which
// is the shape nearly every real module emits.
func segs(parts ...string) []module.Segment {
	out := make([]module.Segment, 0, len(parts))
	for i, p := range parts {
		out = append(out, module.Segment{
			Text:   p,
			Region: fmt.Sprintf("p%d", i),
			Style:  vaxis.Style{Foreground: vaxis.RGBColor(200, 200, 200)},
		})
	}
	return out
}

// benchBar is a realistic desktop bar: workspaces, window title and a media
// line on the left, a clock in the middle, and six status modules on the
// right, each with a detail ladder to step down.
func benchBar(cols int) {
	ellipsis := true
	Init(cols, 1, config.BarSettings{
		TruncatePriority: []string{"right", "left", "middle"},
		EnableEllipsis:   &ellipsis,
		Ellipsis:         "…",
		Gap:              " ",
		ShrinkMin:        3,
	}, vaxis.Style{})
	SetSlotCounts(3, 1, 6)
	SetSpacerSlots(
		[]bool{false, false, false},
		[]bool{false},
		[]bool{false, false, false, false, false, false},
	)
	SetSlotPriorities(
		[]int{0, 1, 2},
		[]int{3},
		[]int{4, 5, 6, 7, 8, 9},
	)

	left := [][][]module.Segment{
		{segs("󰲠 1", " 󰲢 2", " 󰲤 3", " 󰲦 4")},
		{
			segs("󰖟 ", "Squeeze the kitty-only path: pawbar and the vaxis fork"),
			segs("󰖟 ", "Squeeze the kitty-only path"),
			segs("󰖟 "),
		},
		{
			segs("󰝚 ", "Talk Talk - It's My Life"),
			segs("󰝚 ", "Talk Talk"),
		},
	}
	mid := [][][]module.Segment{
		{
			segs("󰃭 ", "Thu 17 Sep ", "󰥔 ", "14:32:07"),
			segs("󰥔 ", "14:32"),
		},
	}
	right := [][][]module.Segment{
		{segs("󰄨 ", "cpu 12%"), segs("󰄨 ", "12%")},
		{segs("󰍛 ", "mem 7.4G"), segs("󰍛 ", "7.4G")},
		{segs("󰔏 ", "54°C")},
		{segs("󰕾 ", "vol 68%"), segs("󰕾 ", "68%")},
		{segs("󰤨 ", "kolkata-5g"), segs("󰤨 ")},
		{segs("󰂀 ", "87% 3h12m"), segs("󰂀 ", "87%")},
	}
	for idx, ladder := range left {
		SetSnapshot(0, idx, ladder)
	}
	for idx, ladder := range mid {
		SetSnapshot(1, idx, ladder)
	}
	for idx, ladder := range right {
		SetSnapshot(2, idx, ladder)
	}
}

// BenchmarkBarLayout is the whole per-frame cost pawbar pays: flatten every
// slot, fit three sides against the width, and write the cells out.
func BenchmarkBarLayout(b *testing.B) {
	const cols = 240
	win := vaxis.NewOffscreenWindow(cols, 1)
	benchBar(cols)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i += 1 {
		Render(win)
	}
}

// BenchmarkBarLayoutNarrow forces the fitting pass to step levels down and
// re-flatten, which is the expensive path.
func BenchmarkBarLayoutNarrow(b *testing.B) {
	const cols = 80
	win := vaxis.NewOffscreenWindow(cols, 1)
	benchBar(cols)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i += 1 {
		Render(win)
	}
}
