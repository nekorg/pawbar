package core

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/pkg/module"
	"github.com/rs/zerolog"
	"go.rockorager.dev/vaxis"
)

// testMod counts renders and exposes a channel source plus a verb.
type testMod struct {
	mu      sync.Mutex
	events  []string
	verbErr error
}

func (m *testMod) note(s string) {
	m.mu.Lock()
	m.events = append(m.events, s)
	m.mu.Unlock()
}

func (m *testMod) Init(ctx *module.Ctx) error {
	module.On(ctx, module.Chan(testFeed), func(s string) {
		m.note("feed:" + s)
		ctx.SetState("busy", s == "busy")
	})
	ctx.HandleVerb("do-it", func(a module.VerbArgs) error {
		m.note("verb:" + a.Region)
		return m.verbErr
	})
	m.note("init")
	return nil
}

func (m *testMod) Render(w *module.Writer) {
	m.mu.Lock()
	n := len(m.events)
	m.mu.Unlock()
	w.Raw(fmt.Sprintf("t%d", n), module.Region("r1"))
}

func (m *testMod) OnHover(ctx *module.Ctx, entered bool, region string) {
	m.note(fmt.Sprintf("hover:%v:%s", entered, region))
}

func (m *testMod) Stop(ctx *module.Ctx) { m.note("stop") }

var (
	testFeed    = make(chan string, 8)
	testModInst *testMod
	registerT   sync.Once
)

func testRegister() {
	registerT.Do(func() {
		module.Register(module.Def{
			Name: "coretest",
			New:  func() module.Module { testModInst = &testMod{}; return testModInst },
			States: []module.StateDef{
				{Name: "busy"},
			},
			Verbs: []module.VerbDef{{Name: "do-it"}},
			Defaults: []byte(`
states:
  busy: { fg: "#ff0000" }
on:
  left: do-it
`),
		})
	})
}

func startTestEngine(t *testing.T, yamlSrc string) (*Engine, *config.Bar) {
	t.Helper()
	testRegister()
	f, issues := config.Load([]byte(yamlSrc), "test.yaml")
	bar, ci := config.Compile(f)
	issues = append(issues, ci...)
	if len(issues) > 0 {
		t.Fatalf("config issues: %v", issues.Err())
	}
	e := New(bar, zerolog.New(zerolog.NewTestWriter(t)))
	e.Start()
	t.Cleanup(e.Stop)
	return e, bar
}

func waitUpdate(t *testing.T, e *Engine, timeout time.Duration) Update {
	t.Helper()
	select {
	case u := <-e.Updates():
		return u
	case <-time.After(timeout):
		t.Fatal("no update within timeout")
		return Update{}
	}
}

func drainText(t *testing.T, e *Engine, want string) Update {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case u := <-e.Updates():
			if len(u.Widest()) > 0 && u.Widest()[0].Text == want {
				return u
			}
		case <-deadline:
			t.Fatalf("never saw segment text %q", want)
		}
	}
}

func TestEngineLifecycle(t *testing.T) {
	e, _ := startTestEngine(t, "right: [coretest]\n")

	// Init render arrives.
	u := waitUpdate(t, e, 2*time.Second)
	if u.Side != Right || u.Index != 0 || len(u.Widest()) == 0 {
		t.Fatalf("bad first update: %+v", u)
	}

	// Source delivery triggers redraw: init + feed:ping = 2 events.
	testFeed <- "ping"
	drainText(t, e, "t2")
}

func TestEngineHoverAndActions(t *testing.T) {
	e, _ := startTestEngine(t, "right: [coretest]\n")
	waitUpdate(t, e, 2*time.Second)

	press := vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress}
	e.Mouse(Right, 0, "r1", press, true)

	// Wait for verb to be recorded.
	ok := false
	for range 40 {
		time.Sleep(25 * time.Millisecond)
		testModInst.mu.Lock()
		for _, ev := range testModInst.events {
			if ev == "verb:r1" {
				ok = true
			}
		}
		testModInst.mu.Unlock()
		if ok {
			break
		}
	}
	if !ok {
		t.Fatalf("default-on left press did not run verb; events: %v", testModInst.events)
	}

	// Hover exit clears hover state.
	e.PointerLeft()
	sawExit := false
	for range 40 {
		time.Sleep(25 * time.Millisecond)
		testModInst.mu.Lock()
		for _, ev := range testModInst.events {
			if ev == "hover:false:r1" {
				sawExit = true
			}
		}
		testModInst.mu.Unlock()
		if sawExit {
			break
		}
	}
	if !sawExit {
		t.Fatalf("no hover exit; events: %v", testModInst.events)
	}
}

func TestEngineConditionStateStyling(t *testing.T) {
	e, _ := startTestEngine(t, "right: [coretest]\n")
	waitUpdate(t, e, 2*time.Second)

	testFeed <- "busy"
	deadline := time.After(2 * time.Second)
	for {
		select {
		case u := <-e.Updates():
			if len(u.Widest()) > 0 && u.Widest()[0].Style.Foreground == module.MustColor("#ff0000").Go() {
				return // busy state styling applied via SetState from handler
			}
		case <-deadline:
			t.Fatal("busy state styling never appeared")
		}
	}
}

func TestEngineUnknownModuleChips(t *testing.T) {
	testRegister()
	f, _ := config.Load([]byte("right: [nope]\n"), "test.yaml")
	bar, issues := config.Compile(f)
	if len(issues) == 0 {
		t.Fatal("expected issues")
	}
	e := New(bar, zerolog.New(zerolog.NewTestWriter(t)))
	e.Start()
	t.Cleanup(e.Stop)

	u := waitUpdate(t, e, 2*time.Second)
	if len(u.Widest()) == 0 || u.Widest()[0].Text != "⚠nope" {
		t.Fatalf("expected error chip, got %+v", u.Widest())
	}

	// Interacting with a chip must not wedge anything.
	for range 40 {
		e.Mouse(Right, 0, "error", vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress}, true)
		e.PointerLeft()
	}
}
