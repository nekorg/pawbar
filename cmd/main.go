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

	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/core"
	"github.com/nekorg/pawbar/internal/logging"
	_ "github.com/nekorg/pawbar/internal/modules/builtin"
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

// Pawbar parses flags and launches the bar panel. The kitty child process
// never reaches this function (katnip intercepts it at init), so settings
// travel to the panel through PAWBAR_* environment variables.
func Pawbar() {
	cfgFlag := flag.String("config", "", "config file `path` (default ~/.config/pawbar/pawbar.yaml)")
	strictFlag := flag.Bool("strict", false, "refuse to start on any config issue")
	checkFlag := flag.Bool("check", false, "validate the config and exit")
	resolvedFlag := flag.Bool("resolved", false, "print the resolved per-slot configuration and exit")
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
		os.Exit(dumpResolved())
	}
	if args := flag.Args(); len(args) > 0 {
		if args[0] == "defaults" {
			os.Exit(printDefaults(args[1:]))
		}
		fmt.Fprintf(os.Stderr, "unknown command %q (did you mean `pawbar defaults`?)\n", args[0])
		os.Exit(2)
	}

	panel := katnip.NewPanel(
		"pawbar",
		katnip.Config{
			Size:        katnip.Vector{X: 0, Y: 1},
			FocusPolicy: katnip.FocusNotAllowed,
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
		},
	)

	go io.Copy(os.Stdout, panel.Reader())
	panel.Run()
}

func configPath() string {
	if p := os.Getenv("PAWBAR_CONFIG"); p != "" {
		return p
	}
	return os.Getenv("HOME") + "/.config/pawbar/pawbar.yaml"
}

// checkConfig validates the config and reports every issue; exit status 1
// when any exist.
func checkConfig() int {
	path := configPath()
	f, issues := config.Read(path)
	_, ci := config.Compile(f)
	issues = append(issues, ci...)
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "%s:%s\n", path, issue.Error())
	}
	if len(issues) > 0 {
		return 1
	}
	fmt.Printf("%s: OK\n", path)
	return 0
}

func mainLoop(kitty *katnip.Kitty, rw io.ReadWriter) int {
	log := logging.Setup(rw)

	strictEnv := os.Getenv("PAWBAR_STRICT") != ""

	f, issues := config.Read(configPath())
	bar, compileIssues := config.Compile(f)
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
			newBar, ci := config.Compile(nf)
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
