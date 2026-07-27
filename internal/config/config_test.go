package config

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nekorg/pawbar/pkg/module"
)

// Test module: a fake "clock" and "volume" mirroring the real archetypes.

type testClockOpts struct {
	Tick     module.Duration `yaml:"tick"`
	AutoTick bool            `yaml:"auto_tick"`
}

type nopModule struct{}

func (nopModule) Init(*module.Ctx) error { return nil }
func (nopModule) Render(*module.Writer)  {}

var registerOnce sync.Once

func registerTestModules(t *testing.T) {
	t.Helper()
	registerOnce.Do(func() {
		module.Register(module.Def{
			Name:    "tclock",
			New:     func() module.Module { return nopModule{} },
			Options: func() any { return &testClockOpts{} },
			Placeholders: []module.Placeholder{
				{Name: "time", Kind: module.KindTime},
			},
			Defaults: []byte(`
format: "{time:%H:%M}"
fg: "#101010"
tick: 5s
auto_tick: true
`),
		})
		module.Register(module.Def{
			Name: "tvolume",
			New:  func() module.Module { return nopModule{} },
			States: []module.StateDef{
				{Name: "muted"},
			},
			Verbs: []module.VerbDef{
				{Name: "toggle-mute"}, {Name: "volume-up"}, {Name: "volume-down"},
			},
			Defaults: []byte(`
format: "{vol}%"
states:
  muted: { format: "muted" }
on:
  left: toggle-mute
`),
		})
		module.Register(module.Static("tsep", "|"))
	})
}

func compileString(t *testing.T, src string) (*Bar, Issues) {
	t.Helper()
	registerTestModules(t)
	f, issues := Load([]byte(src), "test.yaml")
	bar, cIssues := Compile(f)
	return bar, append(issues, cIssues...)
}

func mustClean(t *testing.T, src string) *Bar {
	t.Helper()
	bar, issues := compileString(t, src)
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}
	return bar
}

func TestCompileMinimal(t *testing.T) {
	bar := mustClean(t, "right: [tclock]\n")
	if len(bar.Right) != 1 || bar.Right[0].Name != "tclock" {
		t.Fatalf("bad slots: %+v", bar.Right)
	}
	inst := bar.Right[0]
	if inst.Err != nil {
		t.Fatalf("unexpected instance error: %v", inst.Err)
	}
	b := inst.Table.ResolveBlock(nil)
	if b.Format == nil || b.Format.String() != "{time:%H:%M}" {
		t.Errorf("default format not applied: %+v", b.Format)
	}
	o, err := inst.Table.ResolveOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.(*testClockOpts).Tick.Go() != 5*time.Second {
		t.Errorf("module default options not applied")
	}
}

func TestThemeCascadeAndVars(t *testing.T) {
	bar := mustClean(t, `
theme:
  vars: { accent: "#7aa2f7" }
  defaults:
    fg: "@accent"
    states:
      hover: { bold: true }
right:
  - tclock:
      states:
        hover: { fg: "#ffffff" }
`)
	inst := bar.Right[0]

	base := inst.Table.ResolveBlock(nil)
	if base.Fg == nil || base.Fg.Go() != module.MustColor("#7aa2f7").Go() {
		t.Errorf("theme var + defaults fg not applied: %+v", base.Fg)
	}

	hover := inst.Table.ResolveBlock([]string{HoverState})
	if hover.Bold == nil || !*hover.Bold {
		t.Errorf("theme hover state should cascade")
	}
	if hover.Fg == nil || hover.Fg.Go() != module.MustColor("#ffffff").Go() {
		t.Errorf("entry hover fg should override theme fg")
	}
}

func TestStateOptionOverride(t *testing.T) {
	bar := mustClean(t, `
right:
  - tclock:
      tick: 1s
      states:
        slow: { tick: 1m, fg: "#000000" }
`)
	inst := bar.Right[0]

	o, err := inst.Table.ResolveOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.(*testClockOpts).Tick.Go() != time.Second {
		t.Errorf("base tick: got %v", o.(*testClockOpts).Tick.Go())
	}

	o2, err := inst.Table.ResolveOptions([]string{"slow"})
	if err != nil {
		t.Fatal(err)
	}
	if o2.(*testClockOpts).Tick.Go() != time.Minute {
		t.Errorf("state tick override: got %v", o2.(*testClockOpts).Tick.Go())
	}
	if !o2.(*testClockOpts).AutoTick {
		t.Errorf("unset option keys must keep module defaults through state merge")
	}
	if !inst.Table.OptionStates() {
		t.Errorf("OptionStates should report true")
	}
}

func TestStatePriorityOrder(t *testing.T) {
	bar := mustClean(t, `
right:
  - tvolume:
      states:
        muted: { fg: "#111111" }
        hover: { fg: "#222222" }
`)
	inst := bar.Right[0]
	both := inst.Table.ResolveBlock([]string{"muted", HoverState})
	if both.Fg.Go() != module.MustColor("#222222").Go() {
		t.Errorf("hover must outrank condition states")
	}
	muted := inst.Table.ResolveBlock([]string{"muted"})
	if muted.Fg.Go() != module.MustColor("#111111").Go() {
		t.Errorf("muted styling should apply alone")
	}
	if muted.Format.String() != "muted" {
		t.Errorf("def state default format should survive: %q", muted.Format.String())
	}
}

func TestUnknownKeysGetHints(t *testing.T) {
	_, issues := compileString(t, `
right:
  - tclock:
      formt: "{time:%S}"
`)
	if len(issues) == 0 {
		t.Fatal("want an issue for unknown key")
	}
	found := false
	for _, i := range issues {
		if strings.Contains(i.Msg, `"formt"`) && strings.Contains(i.Hint, `"format"`) {
			found = true
			if i.Line == 0 {
				t.Errorf("issue should carry a line number: %+v", i)
			}
		}
	}
	if !found {
		t.Errorf("no did-you-mean for formt: %v", issues)
	}
}

func TestUnknownModule(t *testing.T) {
	bar, issues := compileString(t, "right: [tclok]\n")
	if len(issues) == 0 {
		t.Fatal("want an issue")
	}
	if bar.Right[0].Err == nil {
		t.Fatal("instance must carry its error for the chip")
	}
	if !strings.Contains(issues[0].Hint, "tclock") {
		t.Errorf("want did-you-mean tclock, got %+v", issues[0])
	}
}

// sep and space are gone; a config still using them gets told what to
// write rather than a did-you-mean for whatever happens to be closest.
func TestRemovedModulesPointAtGap(t *testing.T) {
	_, issues := compileString(t, "right: [sep, space]\n")
	if len(issues) != 2 {
		t.Fatalf("want an issue per entry, got %v", issues.Err())
	}
	for _, i := range issues {
		if !strings.Contains(i.Hint, "gap") {
			t.Errorf("hint should point at gap, got %+v", i)
		}
	}
}

func TestUnknownVerbAndState(t *testing.T) {
	_, issues := compileString(t, `
right:
  - tvolume:
      on:
        left: togle-mute
        right: { set: mutedd }
`)
	var verbIssue, stateIssue bool
	for _, i := range issues {
		if strings.Contains(i.Msg, `"togle-mute"`) {
			verbIssue = true
		}
		if strings.Contains(i.Msg, `"mutedd"`) {
			stateIssue = true
		}
	}
	if !verbIssue || !stateIssue {
		t.Errorf("want verb+state issues, got %v", issues)
	}
}

func TestShippedBindingOverridden(t *testing.T) {
	bar := mustClean(t, `
right:
  - tvolume:
      on:
        left: volume-up
`)
	on := bar.Right[0].On
	if len(on["left"]) != 1 || on["left"][0].Verb != "volume-up" {
		t.Errorf("entry binding must override the shipped default: %+v", on["left"])
	}
}

func TestDefaultsAreBottomLayer(t *testing.T) {
	bar := mustClean(t, `
theme:
  defaults: { fg: "#222222" }
right:
  - tclock
  - tclock: { fg: "#333333" }
  - tvolume
`)
	if fg := bar.Right[0].Table.ResolveBlock(nil).Fg; fg.Go() != module.MustColor("#222222").Go() {
		t.Errorf("theme must override shipped defaults: %v", fg)
	}
	if fg := bar.Right[1].Table.ResolveBlock(nil).Fg; fg.Go() != module.MustColor("#333333").Go() {
		t.Errorf("entry must override theme: %v", fg)
	}
	if on := bar.Right[2].On; len(on["left"]) != 1 || on["left"][0].Verb != "toggle-mute" {
		t.Errorf("shipped on-binding must apply to bare entries: %+v", on)
	}
	if b := bar.Right[2].Table.ResolveBlock([]string{"muted"}); b.Format.String() != "muted" {
		t.Errorf("shipped state styling must apply: %q", b.Format.String())
	}
}

func TestNullUnbindsShippedBinding(t *testing.T) {
	bar := mustClean(t, `
right:
  - tvolume:
      on:
        left: ~
`)
	if _, bound := bar.Right[0].On["left"]; bound {
		t.Errorf("`left: ~` must remove the shipped binding: %+v", bar.Right[0].On)
	}
}

func TestNullClearsInheritedState(t *testing.T) {
	bar := mustClean(t, `
right:
  - tvolume:
      states:
        muted: ~
`)
	b := bar.Right[0].Table.ResolveBlock([]string{"muted"})
	if b.Format.String() != "{vol}%" {
		t.Errorf("`muted: ~` must drop the shipped state block: %q", b.Format.String())
	}
}

func TestEntryDefaultsFalse(t *testing.T) {
	// Without the shipped layer, format and every option are manual.
	_, issues := compileString(t, `
right:
  - tclock: { defaults: false }
`)
	var wantFormat, wantTick, wantAutoTick bool
	for _, i := range issues {
		if strings.Contains(i.Msg, "needs a `format`") {
			wantFormat = true
		}
		if strings.Contains(i.Msg, `option "tick" must be set`) {
			wantTick = true
		}
		if strings.Contains(i.Msg, `option "auto_tick" must be set`) {
			wantAutoTick = true
		}
	}
	if !wantFormat || !wantTick || !wantAutoTick {
		t.Fatalf("want required format+option issues, got %v", issues)
	}

	bar := mustClean(t, `
right:
  - tclock:
      defaults: false
      format: "{time:%S}"
      tick: 9s
      auto_tick: false
`)
	inst := bar.Right[0]
	if inst.Table.ResolveBlock(nil).Fg != nil {
		t.Errorf("shipped fg must not apply with defaults off")
	}
	o, err := inst.Table.ResolveOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.(*testClockOpts).Tick.Go() != 9*time.Second || o.(*testClockOpts).AutoTick {
		t.Errorf("manual options must apply verbatim with defaults off: %+v", o)
	}
}

func TestBarWideDefaultsFalse(t *testing.T) {
	bar := mustClean(t, `
bar: { defaults: false }
right:
  - tvolume: { format: "{vol}" }
`)
	if len(bar.Right[0].On) != 0 {
		t.Errorf("bar-wide defaults:false must drop shipped bindings: %+v", bar.Right[0].On)
	}
}

func TestActionForms(t *testing.T) {
	bar := mustClean(t, `
right:
  - tvolume:
      on:
        scroll-up: volume-up 5
        right: [{ run: "notify-send hi" }, { notify: "hello" }]
        middle: { cycle: [loud] }
      states:
        loud: { fg: "#ff0000" }
`)
	on := bar.Right[0].On
	if a := on["scroll-up"][0]; a.Verb != "volume-up" || len(a.Args) != 1 || a.Args[0] != "5" {
		t.Errorf("verb args: %+v", a)
	}
	if len(on["right"]) != 2 || on["right"][0].Run[0] != "notify-send" || on["right"][1].Notify != "hello" {
		t.Errorf("action list: %+v", on["right"])
	}
	if len(on["middle"][0].Cycle) != 1 {
		t.Errorf("cycle: %+v", on["middle"])
	}
}

func TestBadTopLevelAndButtons(t *testing.T) {
	_, issues := compileString(t, `
lefft: [tsep]
right:
  - tvolume:
      on:
        wheel-up: volume-up
`)
	var topHint, btnHint bool
	for _, i := range issues {
		if strings.Contains(i.Msg, `"lefft"`) && strings.Contains(i.Hint, `"left"`) {
			topHint = true
		}
		if strings.Contains(i.Msg, `"wheel-up"`) && strings.Contains(i.Hint, `"scroll-up"`) {
			btnHint = true
		}
	}
	if !topHint || !btnHint {
		t.Errorf("want hints for both typos, got %v", issues)
	}
}

func TestEntryHashStability(t *testing.T) {
	src := `
right:
  - tclock: { tick: 3s }
  - tclock: { tick: 4s }
  - tsep
`
	registerTestModules(t)
	f1, _ := Load([]byte(src), "a.yaml")
	f2, _ := Load([]byte(src), "b.yaml")
	if entryHash(f1.Right[0]) != entryHash(f2.Right[0]) {
		t.Errorf("same entry must hash equal across parses")
	}
	if entryHash(f1.Right[0]) == entryHash(f1.Right[1]) {
		t.Errorf("different options must hash differently")
	}
	if entryHash(f1.Right[2]) == entryHash(f1.Right[0]) {
		t.Errorf("bare entry vs options entry must differ")
	}
}

func TestEmptySectionsAreLegal(t *testing.T) {
	bar := mustClean(t, `
bar:
theme:
left:
middle:
right:
  - tclock:
      states:
      on:
`)
	if len(bar.Right) != 1 || bar.Right[0].Err != nil {
		t.Fatalf("null sections must parse as empty: %+v", bar.Right)
	}
}

func TestExampleConfigIsClean(t *testing.T) {
	// The shipped example must always parse; module names in it exist
	// only once real modules are registered, so only structural issues
	// count here.
	registerTestModules(t)
	data, err := os.ReadFile("../../docs/examples/pawbar.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f, issues := Load(data, "docs/examples/pawbar.yaml")
	if len(issues) > 0 {
		t.Fatalf("example config has structural issues: %v", issues.Err())
	}
	if len(f.Left) == 0 || len(f.Right) == 0 {
		t.Fatalf("example config should populate sides")
	}
}

func TestPriorityCascades(t *testing.T) {
	bar, issues := compileString(t, `
right:
  - tclock
  - tclock: { priority: -2 }
`)
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}
	if got := bar.Right[0].Priority; got != 0 {
		t.Errorf("unset priority should default to 0, got %d", got)
	}
	if got := bar.Right[1].Priority; got != -2 {
		t.Errorf("entry priority: got %d want -2", got)
	}
}

// A scalar entry value is shorthand for the format, which is what keeps
// punctuation entries (`- gap: " │ "`) to one line.
func TestScalarEntryIsFormatShorthand(t *testing.T) {
	bar, issues := compileString(t, `
right:
  - tclock: "{time:%H:%M}"
  - tclock: ""
  - tclock:
`)
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}
	want := []string{"{time:%H:%M}", "", "{time:%H:%M}"} // the last is shipped
	for i, w := range want {
		f := bar.Right[i].Table.ResolveBlock(nil).Format
		if f == nil {
			t.Fatalf("entry %d: no format resolved", i)
		}
		if got := f.String(); got != w {
			t.Errorf("entry %d: got %q want %q", i, got, w)
		}
	}
	// A bare `- name:` is still the shipped default, not an empty format.
	if bar.Right[1].Hash == bar.Right[2].Hash {
		t.Error(`- tclock: "" and - tclock: should not hash alike`)
	}
}

// A yaml list of formats compiles into a ladder the layout can step down.
func TestFormatListCompiles(t *testing.T) {
	bar, issues := compileString(t, `
right:
  - tclock:
      format:
        - "{time:%H:%M:%S}"
        - "{time:%H:%M}"
`)
	if len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues.Err())
	}
	f := bar.Right[0].Table.ResolveBlock(nil).Format
	if f == nil {
		t.Fatal("no format resolved")
	}
	if got := f.Levels(); got != 2 {
		t.Fatalf("got %d levels want 2", got)
	}
	// The tick has to keep up with the most detailed level on the ladder.
	if got := f.TimeGranularity(); got != time.Second {
		t.Errorf("granularity: got %v want %v", got, time.Second)
	}
}
