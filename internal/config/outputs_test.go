package config

import (
	"strings"
	"testing"
)

func loadString(t *testing.T, src string) (*File, Issues) {
	t.Helper()
	registerTestModules(t)
	return Load([]byte(src), "test.yaml")
}

// compileFor loads src and compiles it as the bar on output would see it.
func compileFor(t *testing.T, src, output string) (*Bar, Issues) {
	t.Helper()
	f, issues := loadString(t, src)
	bar, cIssues := Compile(f.For(output))
	return bar, append(issues, cIssues...)
}

func names(insts []*Instance) []string {
	out := make([]string, len(insts))
	for i, inst := range insts {
		out[i] = inst.Name
	}
	return out
}

func TestOutputSelForms(t *testing.T) {
	cases := []struct {
		src     string
		matches map[string]bool
	}{
		{"", map[string]bool{"eDP-1": true, "HDMI-A-1": true}},
		{"bar: { outputs: all }\n", map[string]bool{"eDP-1": true, "HDMI-A-1": true}},
		{"bar: { outputs: none }\n", map[string]bool{"eDP-1": false, "HDMI-A-1": false}},
		{"bar: { outputs: eDP-1 }\n", map[string]bool{"eDP-1": true, "HDMI-A-1": false}},
		{"bar:\n  outputs: [eDP-1, DP-3]\n", map[string]bool{"eDP-1": true, "DP-3": true, "HDMI-A-1": false}},
		{"bar:\n  outputs: [eDP-1, all]\n", map[string]bool{"eDP-1": true, "HDMI-A-1": true}},
	}
	for _, c := range cases {
		f, issues := loadString(t, c.src)
		if len(issues) > 0 {
			t.Fatalf("%q: unexpected issues: %v", c.src, issues.Err())
		}
		for name, want := range c.matches {
			if got := f.Bar.Outputs.Matches(name); got != want {
				t.Errorf("%q: Matches(%q) = %v, want %v", c.src, name, got, want)
			}
		}
	}
}

func TestOutputSelDuplicateReported(t *testing.T) {
	_, issues := loadString(t, "bar:\n  outputs: [eDP-1, eDP-1]\n")
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "listed twice") {
		t.Fatalf("want duplicate issue, got %v", issues)
	}
}

func TestOverrideReplacesSide(t *testing.T) {
	src := `
left: [tclock]
right: [tvolume, tclock]
outputs:
  HDMI-A-1:
    right: [tclock]
    left:
`
	bar, issues := compileFor(t, src, "HDMI-A-1")
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}
	if got := names(bar.Right); len(got) != 1 || got[0] != "tclock" {
		t.Errorf("right = %v, want [tclock]", got)
	}
	if got := names(bar.Left); len(got) != 0 {
		t.Errorf("left = %v, want empty (mentioned with nothing under it)", got)
	}

	// Another output keeps the base document.
	base, issues := compileFor(t, src, "eDP-1")
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}
	if got := names(base.Right); len(got) != 2 {
		t.Errorf("eDP-1 right = %v, want the base two", got)
	}
	if got := names(base.Left); len(got) != 1 {
		t.Errorf("eDP-1 left = %v, want the base one", got)
	}
}

func TestOverrideMergesBarAndTheme(t *testing.T) {
	src := `
bar:
  gap: " "
  ellipsis: "…"
theme:
  vars: { accent: "#7aa2f7" }
  defaults:
    fg: "@accent"
    bold: true
    states:
      hover: { bold: true }
right: [tclock]
outputs:
  HDMI-A-1:
    bar: { gap: " | " }
    theme:
      vars: { accent: "#ff0000" }
      defaults:
        states:
          hover: { italic: true }
`
	f, issues := loadString(t, src)
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}

	over := f.For("HDMI-A-1")
	if over.Bar.Gap != " | " {
		t.Errorf("gap = %q, want the override", over.Bar.Gap)
	}
	if over.Bar.Ellipsis != "…" {
		t.Errorf("ellipsis = %q, want the base value kept", over.Bar.Ellipsis)
	}
	if f.Bar.Gap != " " {
		t.Errorf("base bar mutated: gap = %q", f.Bar.Gap)
	}

	bar, cIssues := Compile(over)
	if len(cIssues) > 0 {
		t.Fatalf("unexpected issues: %v", cIssues.Err())
	}
	// bold survives from the base defaults, fg picks up the override var,
	// and the hover state merges rather than being replaced.
	block := bar.Right[0].Table.ResolveBlock([]string{"hover"})
	if block.Bold == nil || !*block.Bold || block.Italic == nil || !*block.Italic {
		t.Errorf("hover block = %+v, want bold (base) and italic (override)", block)
	}

	baseBar, _ := Compile(f)
	baseBlock := baseBar.Right[0].Table.ResolveBlock([]string{"hover"})
	if baseBlock.Italic != nil && *baseBlock.Italic {
		t.Errorf("base theme node mutated by the merge: %+v", baseBlock)
	}
}

func TestOverrideUnknownKeyGetsHint(t *testing.T) {
	_, issues := loadString(t, "outputs:\n  eDP-1:\n    rigth: [tclock]\n")
	if len(issues) != 1 {
		t.Fatalf("want one issue, got %v", issues)
	}
	if !strings.Contains(issues[0].Msg, `unknown key "rigth"`) || !strings.Contains(issues[0].Hint, "right") {
		t.Errorf("issue = %v, want an unknown-key hint pointing at right", issues[0])
	}
}

func TestOverrideForUnselectedOutputReported(t *testing.T) {
	_, issues := loadString(t, "bar: { outputs: eDP-1 }\noutputs:\n  HDMI-A-1:\n    right: [tclock]\n")
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "not selected by bar.outputs") {
		t.Fatalf("want a not-selected issue, got %v", issues)
	}
	if issues[0].Line == 0 {
		t.Errorf("issue has no position: %v", issues[0])
	}
}

func TestOverrideBarValidatedAfterMerge(t *testing.T) {
	_, issues := loadString(t, "outputs:\n  eDP-1:\n    bar: { truncate_priority: [left, left, right] }\n")
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "listed twice") {
		t.Fatalf("want the merged bar to be validated, got %v", issues)
	}
}

func TestOverrideNestedOutputsRejected(t *testing.T) {
	_, issues := loadString(t, "outputs:\n  eDP-1:\n    bar: { outputs: [DP-1] }\n")
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "no effect inside") {
		t.Fatalf("want nested bar.outputs rejected, got %v", issues)
	}
}

func TestForUnknownOutputIsBase(t *testing.T) {
	f, _ := loadString(t, "right: [tclock]\noutputs:\n  eDP-1:\n    right: [tvolume]\n")
	if f.For("DP-9") != f {
		t.Errorf("For with no matching section should return the base file")
	}
}
