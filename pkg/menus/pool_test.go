package menus

import (
	"slices"
	"testing"
)

// counts reads the plan back in the order it was given, so a test can write
// the ladder the way it was specified.
func counts(order []string, target, max int) []int {
	want := plan(order, target, max)
	got := make([]int, len(order))
	for i, output := range order {
		got[i] = want[output]
	}
	return got
}

func TestPlanLadders(t *testing.T) {
	two := []string{"DP-1", "DP-2"}
	three := []string{"DP-1", "DP-2", "DP-3"}

	cases := []struct {
		name   string
		order  []string
		target int
		max    int
		want   []int
	}{
		{"two, pooling off", two, 2, 0, []int{0, 0}},
		{"two, one spare", two, 2, 1, []int{1, 0}},
		{"two, intent only", two, 2, 2, []int{2, 0}},
		{"two, intent plus a hedge", two, 2, 3, []int{2, 1}},
		{"two, both at target", two, 2, 4, []int{2, 2}},
		{"two, above target", two, 2, 5, []int{2, 2}},
		{"two, auto", two, 2, -1, []int{2, 1}},

		{"three, one spare", three, 2, 1, []int{1, 0, 0}},
		{"three, intent only", three, 2, 2, []int{2, 0, 0}},
		{"three, one hedge", three, 2, 3, []int{2, 1, 0}},
		{"three, a hedge each", three, 2, 4, []int{2, 1, 1}},
		{"three, one at target", three, 2, 5, []int{2, 2, 1}},
		{"three, all at target", three, 2, 6, []int{2, 2, 2}},
		{"three, above target", three, 2, 9, []int{2, 2, 2}},
		{"three, auto", three, 2, -1, []int{2, 1, 1}},

		{"target 1", two, 1, -1, []int{1, 1}},
		{"target 1, capped", three, 1, 2, []int{1, 1, 0}},
		{"target 3", three, 3, -1, []int{3, 1, 1}},
		{"target 3, ample", three, 3, 9, []int{3, 3, 3}},

		{"one monitor", []string{"eDP-1"}, 2, -1, []int{2}},
		{"one monitor, capped", []string{"eDP-1"}, 2, 1, []int{1}},
		{"no target", two, 0, -1, []int{0, 0}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := counts(c.order, c.target, c.max); !slices.Equal(got, c.want) {
				t.Fatalf("plan(target=%d, max=%d) = %v, want %v", c.target, c.max, got, c.want)
			}
		})
	}
}

// The budget is a cap on the whole desktop, whatever the ranking is.
func TestPlanNeverExceedsMax(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	for target := range 4 {
		for max := range 12 {
			total := 0
			for _, n := range plan(order, target, max) {
				total += n
			}
			if total > max {
				t.Fatalf("target=%d max=%d planned %d spares", target, max, total)
			}
		}
	}
}

func TestPlanIgnoresOutputsWithNoBudget(t *testing.T) {
	if want := plan([]string{"DP-1", "DP-2"}, 2, 2); len(want) != 1 {
		t.Fatalf("plan = %v, want only the intent output to appear", want)
	}
}
