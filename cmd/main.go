// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/core"
	"github.com/nekorg/pawbar/internal/logging"
	_ "github.com/nekorg/pawbar/internal/modules/builtin"
	"github.com/nekorg/pawbar/internal/monitor"
	"github.com/nekorg/pawbar/internal/tui"
	"github.com/nekorg/pawbar/pkg/menus"
	"github.com/nekorg/pawbar/pkg/module"
	"go.rockorager.dev/vaxis"
)

func init() {
	katnip.RegisterFunc("pawbar", mainLoop)
	// Register the generic menu-host identity last, so every menu kind is
	// in the registry before a re-exec'd host dispatches on it. cmd's init
	// runs after the menus/module packages it imports have registered.
	menus.RegisterHost()
}

// Pawbar parses flags and supervises one bar panel per monitor. The kitty
// child process never reaches this function (katnip intercepts it at
// init), so settings travel to the panel through PAWBAR_* environment
// variables — including PAWBAR_OUTPUT, which tells each panel process
// which monitor it is on.
func Pawbar() {
	cfgFlag := flag.String("config", "", "config file `path` (default ~/.config/pawbar/pawbar.yaml)")
	strictFlag := flag.Bool("strict", false, "refuse to start on any config issue")
	checkFlag := flag.Bool("check", false, "validate the config and exit")
	resolvedFlag := flag.Bool("resolved", false, "print the resolved per-slot configuration and exit")
	var outputFlag outputList
	flag.Var(&outputFlag, "output", "monitor(s) to put a bar on, overriding bar.outputs; repeatable, or `all`/`none`")
	flag.Parse()

	if *cfgFlag != "" {
		os.Setenv("PAWBAR_CONFIG", *cfgFlag)
	}
	if *strictFlag {
		os.Setenv("PAWBAR_STRICT", "1")
	}
	if *checkFlag {
		os.Exit(checkConfig())
	}
	if *resolvedFlag {
		os.Exit(dumpResolved(outputFlag.one()))
	}
	if args := flag.Args(); len(args) > 0 {
		if args[0] == "defaults" {
			os.Exit(printDefaults(args[1:]))
		}
		fmt.Fprintf(os.Stderr, "unknown command %q (did you mean `pawbar defaults`?)\n", args[0])
		os.Exit(2)
	}

	os.Exit(supervise(outputFlag.sel()))
}

// supervise runs the panel supervisor until a signal stops it.
func supervise(flagSel *config.OutputSel) int {
	lock, holder, err := acquireLock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pawbar is already running")
		if holder > 0 {
			fmt.Fprintf(os.Stderr, " (pid %d)", holder)
		}
		fmt.Fprintf(os.Stderr, "\none pawbar puts a bar on every monitor; stop that one first\n(%v)\n", err)
		return 1
	}
	defer lock.Close()

	log := logging.Setup(os.Stderr)

	sel := config.OutputSel{}
	if flagSel != nil {
		sel = *flagSel
	} else {
		f, issues := config.Read(configPath())
		if issues.Fatal() {
			// The panels report config problems in full; the supervisor
			// only needs to know which monitors to cover.
			log.Warn().Msg("config: unreadable, putting a bar on every monitor")
		}
		sel = f.Bar.Outputs
	}
	if sel.Empty() {
		log.Error().Msg("no monitors selected (bar.outputs: none), nothing to do")
		return 1
	}
	log.Info().Msgf("monitors: %s", sel)

	return newSupervisor(log, sel, flagSel).run()
}

// barPanelConfig is the kitty panel every bar runs in, pinned to output.
func barPanelConfig(output string) katnip.Config {
	return katnip.Config{
		Size:        katnip.Vector{X: 0, Y: 1},
		FocusPolicy: katnip.FocusNotAllowed,
		OutputName:  output,
		KittyOverrides: []string{
			"font_size=12",
			"cursor_trail=0",
			"paste_actions=replace-dangerous-control-codes",
			"map kitty_mod+equal  no_op",
			"map kitty_mod+plus   no_op",
			"map kitty_mod+kp_add no_op",
			"map cmd+plus         no_op",
			"map cmd+equal        no_op",
			"map shift+cmd+equal  no_op",
			"map kitty_mod+minus       no_op",
			"map kitty_mod+kp_subtract no_op",
			"map cmd+minus             no_op",
			"map shift+cmd+minus       no_op",
			"map kitty_mod+backspace no_op",
			"map cmd+0               no_op",
		},
	}
}

// outputList collects repeated (or comma-separated) --output flags.
type outputList []string

func (o *outputList) String() string { return strings.Join(*o, ",") }

func (o *outputList) Set(v string) error {
	for _, name := range strings.Split(v, ",") {
		if name = strings.TrimSpace(name); name != "" {
			*o = append(*o, name)
		}
	}
	return nil
}

// sel turns the flag into an output selection, nil when unset so the
// config decides.
func (o outputList) sel() *config.OutputSel {
	if len(o) == 0 {
		return nil
	}
	s := &config.OutputSel{}
	for _, name := range o {
		switch name {
		case "all":
			return &config.OutputSel{All: true}
		case "none":
			return &config.OutputSel{}
		default:
			s.Names = append(s.Names, name)
		}
	}
	return s
}

// one returns the single output named on the command line, for the
// introspection commands that render one bar's configuration.
func (o outputList) one() string {
	if len(o) == 0 {
		return ""
	}
	return o[0]
}

func configPath() string {
	if p := os.Getenv("PAWBAR_CONFIG"); p != "" {
		return p
	}
	return os.Getenv("HOME") + "/.config/pawbar/pawbar.yaml"
}

// checkConfig validates the config and reports every issue; exit status 1
// when any exist. Every per-output section is compiled too: a bar that only
// exists on the second monitor must still be checkable from the first.
func checkConfig() int {
	path := configPath()
	f, issues := config.Read(path)
	_, ci := config.Compile(f)
	issues = append(issues, ci...)
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "%s:%s\n", path, issue.Error())
	}
	bad := len(issues) > 0

	for _, name := range f.OutputNames() {
		_, oi := config.Compile(f.For(name))
		for _, issue := range oi {
			// Issues from the shared base document were reported above.
			if slices.ContainsFunc(ci, func(c config.Issue) bool { return c == issue }) {
				continue
			}
			bad = true
			fmt.Fprintf(os.Stderr, "%s:%s: %s\n", path, name, issue.Error())
		}
	}

	if bad {
		return 1
	}
	fmt.Printf("%s: OK\n", path)
	return 0
}

func mainLoop(kitty *katnip.Kitty, rw io.ReadWriter) int {
	log := logging.Setup(rw)

	strictEnv := os.Getenv("PAWBAR_STRICT") != ""
	output := monitor.Self()
	if output != "" {
		log = log.With().Str("output", output).Logger()
		logging.Log = log
	}

	f, issues := config.Read(configPath())
	bar, compileIssues := config.Compile(f.For(output))
	issues = append(issues, compileIssues...)
	for _, issue := range issues {
		log.Warn().Msgf("config: %s", issue.Error())
	}
	if (bar.Settings.Strict || strictEnv) && len(issues) > 0 {
		log.Error().Msgf("config: strict mode, refusing to start with %d issue(s)", len(issues))
		return 1
	}

	vx, err := vaxis.New(vaxis.Options{EnableSGRPixels: true})
	if err != nil {
		log.Error().Msgf("initializing vaxis: %v", err)
		return 1
	}

	defer func() {
		p := recover()
		vx.Close()
		if p != nil {
			log.Error().Str("stack", string(debug.Stack())).Msgf("unexpected error: %v", p)
		}
	}()

	win := vx.Window()
	win.Clear()

	{
		size := vx.Size()
		menus.SetCellMetrics(size.Cols, size.Rows, size.XPixel, size.YPixel)
	}

	engine := core.New(bar, log)

	w, h := win.Size()
	log.Debug().Msgf("panel size (cells): %d, %d", w, h)
	tui.Init(w, h, bar.Settings, bar.GapStyle)
	tui.SetSlotCounts(engine.SlotCounts())
	tui.SetSpacerSlots(engine.SpacerSlots())
	tui.SetSlotPriorities(engine.SlotPriorities())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	screenEvents := vx.Events()
	userSignals := setupUserSignals()
	resumeCh := watchResume(ctx)

	reloadCh, werr := core.WatchConfig(ctx.Done(), configPath(), log)
	if werr != nil {
		log.Warn().Msgf("config: hot reload disabled: %v", werr)
	}

	engine.Start()
	defer engine.Stop()

	mouseShape := vaxis.MouseShapeDefault

	render := func() {
		tui.Render(win)
		vx.Render()
	}
	fullResize := func() {
		win = vx.Window()
		w, h = win.Size()
		tui.Resize(w, h)
		render()
	}

	render()

	// Warm the first menu spare now that the bar is up, so even the first
	// menu opens without paying the kitty spawn cost.
	menus.PrewarmPool()

	for {
		select {
		case ev := <-screenEvents:
			switch ev := ev.(type) {
			case vaxis.Resize:
				menus.SetCellMetrics(ev.Cols, ev.Rows, ev.XPixel, ev.YPixel)
				// A resize is how a mode or scale change on this output
				// reaches the bar; the cached geometry is now stale.
				monitor.Invalidate()
				fullResize()
				log.Debug().Msgf("panel size: %d, %d", ev.XPixel, ev.YPixel)
			case vaxis.Redraw:
				render()
			case vaxis.Key:
				if ev.String() == "Ctrl+c" {
					vx.PostEvent(vaxis.QuitEvent{})
				}
			case vaxis.FocusOut:
				engine.PointerLeft()
				updateMouseShape(vx, vaxis.MouseShapeDefault, &mouseShape)
			case vaxis.Mouse:
				if ev.EventType == vaxis.EventLeave {
					engine.PointerLeft()
					updateMouseShape(vx, vaxis.MouseShapeDefault, &mouseShape)
					continue
				}
				leftHalf := true
				if sz := vx.Size(); sz.XPixel > 0 && sz.Cols > 0 {
					// Pixel offset within the clicked cell; inverts
					// vaxis's own Col = XPixel*Cols/XPixel_total.
					rem := (ev.XPixel * sz.Cols) % sz.XPixel
					leftHalf = rem*2 < sz.XPixel
				}
				hit, ok := tui.HitAt(ev.Col, leftHalf)
				engine.Mouse(core.Side(hit.Side), hit.Index, hit.Region, ev, ok)
				shape := vaxis.MouseShapeDefault
				if ok {
					shape = hit.Shape
				}
				updateMouseShape(vx, shape, &mouseShape)
			case vaxis.QuitEvent:
				log.Info().Msg("received exit signal")
				return 0
			}

		case u := <-engine.Updates():
			tui.SetSnapshot(int(u.Side), u.Index, u.Levels)
			// Coalesce whatever else is already queued before rendering.
			for {
				select {
				case u = <-engine.Updates():
					tui.SetSnapshot(int(u.Side), u.Index, u.Levels)
					continue
				default:
				}
				break
			}
			render()

		case s := <-userSignals:
			log.Debug().Msgf("full render: %s", canonicalSignalName(s))
			fullResize()

		case resumeEv := <-resumeCh:
			log.Info().Msgf("waking from suspend (%s)", resumeEv.Source)
			engine.Wake()
			fullResize()

		case <-reloadCh:
			nf, nIssues := config.Read(configPath())
			newBar, ci := config.Compile(nf.For(output))
			nIssues = append(nIssues, ci...)
			for _, issue := range nIssues {
				log.Warn().Msgf("config: %s", issue.Error())
			}
			strict := newBar.Settings.Strict || strictEnv
			if nIssues.Fatal() || (strict && len(nIssues) > 0) {
				log.Warn().Msg("config: reload rejected, keeping previous configuration")
				continue
			}
			log.Info().Msg("config: reloading")
			bar = newBar
			engine.Reload(bar)
			tui.Init(w, h, bar.Settings, bar.GapStyle)
			tui.SetSlotCounts(engine.SlotCounts())
			tui.SetSpacerSlots(engine.SpacerSlots())
			tui.SetSlotPriorities(engine.SlotPriorities())
			// Reseed the fresh slot tables with every runner's last
			// output: kept modules must not blank out until their next
			// event.
			engine.Snapshots(func(side core.Side, idx int, levels [][]module.Segment) {
				tui.SetSnapshot(int(side), idx, levels)
			})
			render()

		case s := <-engine.Restarts():
			engine.RestartSlot(s)
		}
	}
}

func updateMouseShape(vx *vaxis.Vaxis, target vaxis.MouseShape, old *vaxis.MouseShape) {
	if target == "" {
		target = vaxis.MouseShapeDefault
	}
	if *old == target {
		return
	}
	*old = target
	vx.SetMouseShape(target)
	vx.Render()
}
