package ws

import (
	"slices"
	"strings"
	"testing"

	"github.com/nekorg/pawbar/internal/services/hypr"
	"github.com/nekorg/pawbar/internal/services/i3"
	"github.com/nekorg/pawbar/pkg/module"
)

func wsNames(list []Workspace) []string {
	out := make([]string, len(list))
	for i, w := range list {
		out[i] = w.Name
	}
	return out
}

func TestViewFiltersByMonitor(t *testing.T) {
	list := []Workspace{
		{ID: 1, Name: "1", Monitor: "eDP-1"},
		{ID: 2, Name: "2", Monitor: "HDMI-A-1"},
		{ID: 3, Name: "3", Monitor: "eDP-1"},
	}
	cases := []struct {
		mode, self string
		want       []string
	}{
		{monitorSelf, "eDP-1", []string{"1", "3"}},
		{monitorSelf, "HDMI-A-1", []string{"2"}},
		{monitorSelf, "", []string{"1", "2", "3"}}, // not pinned: show everything
		{monitorAll, "eDP-1", []string{"1", "2", "3"}},
		{"", "eDP-1", []string{"1", "2", "3"}},
		{"HDMI-A-1", "eDP-1", []string{"2"}},
		{"DP-9", "eDP-1", nil}, // a monitor that is not there
	}
	for _, c := range cases {
		got := wsNames(view(list, c.mode, c.self))
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("view(mode=%q, self=%q) = %v, want %v", c.mode, c.self, got, c.want)
		}
	}
}

func TestViewKeepsWorkspacesWithoutAMonitor(t *testing.T) {
	list := []Workspace{{ID: 1, Name: "1"}, {ID: 2, Name: "2", Monitor: "HDMI-A-1"}}
	if got := wsNames(view(list, monitorSelf, "eDP-1")); !slices.Equal(got, []string{"1"}) {
		t.Errorf("view = %v, want the unattributed workspace kept", got)
	}
}

func TestHyprListPerMonitorFocus(t *testing.T) {
	workspaces := []hypr.Workspace{
		{Id: 1, Name: "1", Monitor: "eDP-1", MonitorID: 0},
		{Id: 2, Name: "2", Monitor: "HDMI-A-1", MonitorID: 1},
		{Id: 3, Name: "3", Monitor: "eDP-1", MonitorID: 0},
	}
	monitors := []hypr.Monitor{
		{ID: 0, Name: "eDP-1", ActiveWorkspace: hypr.ClientWS{Id: 1, Name: "1"}},
		{ID: 1, Name: "HDMI-A-1", ActiveWorkspace: hypr.ClientWS{Id: 2, Name: "2"}, Focused: true},
	}

	list := hyprList(workspaces, monitors, map[int]bool{})
	byID := map[int]Workspace{}
	for _, w := range list {
		byID[w.ID] = w
	}

	if !byID[2].Active || !byID[2].Visible {
		t.Errorf("workspace on the focused monitor = %+v, want active", byID[2])
	}
	// The pre-multi-monitor bug: this used to be inactive *and* unmarked,
	// leaving the other monitor's bar with nothing highlighted.
	if byID[1].Active || !byID[1].Visible {
		t.Errorf("workspace on the unfocused monitor = %+v, want visible but not active", byID[1])
	}
	if byID[3].Active || byID[3].Visible {
		t.Errorf("off-screen workspace = %+v, want neither", byID[3])
	}
}

func TestHyprListSpecialWorkspaceHoldsFocus(t *testing.T) {
	workspaces := []hypr.Workspace{
		{Id: 1, Name: "1", Monitor: "eDP-1"},
		{Id: -98, Name: "special:magic", Monitor: "eDP-1"},
	}
	monitors := []hypr.Monitor{{
		ID:               0,
		Name:             "eDP-1",
		ActiveWorkspace:  hypr.ClientWS{Id: 1, Name: "1"},
		SpecialWorkspace: hypr.ClientWS{Id: -98, Name: "special:magic"},
		Focused:          true,
	}}

	list := hyprList(workspaces, monitors, map[int]bool{})
	if list[0].ID != -98 {
		t.Fatalf("order = %v, want the special workspace first", wsNames(list))
	}
	special, normal := list[0], list[1]
	if !special.Special || !special.Active || !special.Visible {
		t.Errorf("special = %+v, want the focused, visible special workspace", special)
	}
	if normal.Active {
		t.Errorf("normal = %+v, want focus on the open special workspace", normal)
	}
	if !normal.Visible {
		t.Errorf("normal = %+v, want it still counted as on screen", normal)
	}
}

func TestHyprListDisabledMonitorIgnored(t *testing.T) {
	workspaces := []hypr.Workspace{{Id: 1, Name: "1", Monitor: "eDP-1"}}
	monitors := []hypr.Monitor{
		{ID: 0, Name: "eDP-1", ActiveWorkspace: hypr.ClientWS{Id: 1}, Focused: true},
		{ID: 1, Name: "HDMI-A-1", ActiveWorkspace: hypr.ClientWS{Id: 1}, Disabled: true, Focused: true},
	}
	if list := hyprList(workspaces, monitors, map[int]bool{}); !list[0].Active {
		t.Errorf("list = %+v, want the enabled monitor to decide focus", list)
	}
}

func TestHyprListUrgencyIsStickyUntilSeen(t *testing.T) {
	workspaces := []hypr.Workspace{
		{Id: 1, Name: "1", Monitor: "eDP-1"},
		{Id: 2, Name: "2", Monitor: "eDP-1"},
	}
	monitors := []hypr.Monitor{
		{ID: 0, Name: "eDP-1", ActiveWorkspace: hypr.ClientWS{Id: 1}, Focused: true},
	}
	urgent := map[int]bool{2: true, 9: true} // 9 no longer exists

	list := hyprList(workspaces, monitors, urgent)
	if !list[1].Urgent {
		t.Errorf("workspace 2 = %+v, want urgency preserved across the rebuild", list[1])
	}
	if urgent[9] {
		t.Errorf("urgency kept for a workspace that is gone: %v", urgent)
	}

	// Focusing it clears the flag.
	monitors[0].ActiveWorkspace = hypr.ClientWS{Id: 2}
	if list = hyprList(workspaces, monitors, urgent); list[1].Urgent {
		t.Errorf("workspace 2 = %+v, want urgency cleared once focused", list[1])
	}
}

func TestHyprRegionRoundTrip(t *testing.T) {
	b := &hyprBackend{}
	if got := b.Region(Workspace{ID: 3, Name: "3"}); got != "3" {
		t.Errorf("region = %q, want the id", got)
	}
	// A named workspace has a negative id; a negative id handed to the
	// dispatch is a relative move, so the click must go by name.
	got := b.Region(Workspace{ID: -1342, Name: "web"})
	if got != "web" {
		t.Errorf("named region = %q, want the name", got)
	}
	// Special workspaces are switched by name: "workspace -98" does
	// nothing, which is why clicking one never worked.
	got = b.Region(Workspace{ID: -98, Name: "special:magic", Special: true})
	if got != "special:magic" {
		t.Errorf("special region = %q, want the name", got)
	}
	if name, ok := strings.CutPrefix(got, "special:"); !ok || name != "magic" {
		t.Errorf("special region %q does not decode to a dispatcher name", got)
	}
}

func TestI3OrderNumberedThenNamed(t *testing.T) {
	list := []Workspace{
		{ID: -1, Name: "web", Monitor: "HDMI-A-1"},
		{ID: 2, Name: "2", Monitor: "HDMI-A-1"},
		{ID: -1, Name: "chat", Monitor: "eDP-1"},
		{ID: 1, Name: "1", Monitor: "eDP-1"},
	}
	sortI3(list)
	if got := wsNames(list); !slices.Equal(got, []string{"1", "2", "chat", "web"}) {
		t.Errorf("order = %v, want numbered ascending then named alphabetically", got)
	}
}

// i3 reports num -1 for every named workspace, so keying the cache by
// number used to collapse them into one entry.
func TestI3NamedWorkspacesDoNotCollide(t *testing.T) {
	workspaces := []i3.Workspace{
		{Id: -1, Name: "web", Output: "eDP-1", Visible: true, Focused: true},
		{Id: -1, Name: "chat", Output: "HDMI-A-1", Visible: true},
		{Id: -1, Name: "mail", Output: "HDMI-A-1"},
	}
	list := make([]Workspace, 0, len(workspaces))
	for _, w := range workspaces {
		list = append(list, Workspace{
			ID: w.Id, Name: w.Name, Monitor: w.Output,
			Active: w.Focused, Visible: w.Visible, Urgent: w.Urgent,
		})
	}
	sortI3(list)
	if got := wsNames(list); !slices.Equal(got, []string{"chat", "mail", "web"}) {
		t.Fatalf("list = %v, want all three named workspaces", got)
	}
	if got := wsNames(view(list, monitorSelf, "HDMI-A-1")); !slices.Equal(got, []string{"chat", "mail"}) {
		t.Errorf("view = %v, want that output's two workspaces", got)
	}
}

// fakeBackend renders a fixed list without a compositor.
type fakeBackend struct{ list []Workspace }

func (f *fakeBackend) List() []Workspace         { return f.list }
func (f *fakeBackend) Events() <-chan struct{}   { return nil }
func (f *fakeBackend) Region(w Workspace) string { return w.Name }
func (f *fakeBackend) Goto(string)               {}
func (f *fakeBackend) Close()                    {}

// statesFormatter renders the states a segment was emitted with, so the
// test can assert on them through the normal Writer path.
type statesFormatter struct{ states []string }

func (s statesFormatter) Render(module.P) (string, error) { return strings.Join(s.states, "+"), nil }
func (s statesFormatter) Levels() int                     { return 1 }
func (s statesFormatter) RenderParts(int, module.P) ([]module.Part, error) {
	text, _ := s.Render(nil)
	return []module.Part{{Text: text}}, nil
}

func renderStates(t *testing.T, m *wsModule) map[string]string {
	t.Helper()
	w := module.NewWriter(func(extra []string) module.Resolved {
		return module.Resolved{Formatter: statesFormatter{states: extra}}
	})
	m.Render(w)

	out := map[string]string{}
	for _, seg := range w.Segments() {
		out[seg.Region] = seg.Text
	}
	return out
}

func TestRenderStatesPerMonitor(t *testing.T) {
	list := []Workspace{
		{ID: 1, Name: "1", Monitor: "eDP-1", Visible: true},
		{ID: 2, Name: "2", Monitor: "eDP-1", Urgent: true},
		{ID: 3, Name: "3", Monitor: "HDMI-A-1", Active: true, Visible: true},
	}
	m := &wsModule{b: &fakeBackend{list: list}, list: list, monitorOpt: monitorAll}

	got := renderStates(t, m)
	want := map[string]string{"1": "visible", "2": "urgent", "3": "active"}
	for region, states := range want {
		if got[region] != states {
			t.Errorf("workspace %s rendered with %q, want %q", region, got[region], states)
		}
	}
}

func TestRenderCurrentOnlyKeepsWhatIsOnScreen(t *testing.T) {
	list := []Workspace{
		{ID: 1, Name: "1", Monitor: "eDP-1", Visible: true},
		{ID: 2, Name: "2", Monitor: "eDP-1"},
		{ID: 3, Name: "3", Monitor: "HDMI-A-1", Active: true, Visible: true},
	}
	m := &wsModule{b: &fakeBackend{list: list}, list: list, monitorOpt: monitorAll, currentOnly: true}

	got := renderStates(t, m)
	if len(got) != 2 || got["1"] == "" || got["3"] == "" {
		t.Errorf("current_only rendered %v, want the displayed workspace of each monitor", got)
	}

	// Scoped to one monitor it is a single segment again.
	m.monitorOpt, m.self = monitorSelf, "eDP-1"
	if got := renderStates(t, m); len(got) != 1 || got["1"] != "visible" {
		t.Errorf("current_only on one monitor rendered %v, want just its own", got)
	}
}
