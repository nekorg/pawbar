// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package main

import (
	"context"
	"io"
	"log"
	"os"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
	_ "github.com/nekorg/pawbar/internal/modules/all"
	"github.com/nekorg/pawbar/internal/tui"
	"github.com/nekorg/pawbar/internal/utils"
)

func init() {
	katnip.RegisterFunc("pawbar", mainLoop)
}

func main() {
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

func mainLoop(kitty *katnip.Kitty, rw io.ReadWriter) int {
	cfgPath := os.Getenv("HOME") + "/.config/pawbar/pawbar.yaml"

	utils.Logger = log.New(rw, "", log.LstdFlags)

	vx, err := vaxis.New(vaxis.Options{EnableSGRPixels: true})
	if err != nil {
		utils.Logger.Println("There was an error initializing Vaxis.")
		return 1
	}

	defer func() {
		err := recover()
		vx.Close()
		if err != nil {
			utils.Logger.Printf("unexpected error: %v\n", err)
		}
	}()

	win := vx.Window()
	win.Clear()

	cfg, err := config.Parse(cfgPath)
	if err != nil {
		utils.Logger.Printf("config error: %v\n", err)
		return 1
	}

	l, m, r := config.InstantiateModules(cfg)

	modev, l, m, r := modules.Init(l, m, r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	screenEvents := vx.Events()
	userSignals := setupUserSignals()
	resumeCh := watchResume(ctx)

	var prevHoverMod modules.Module
	var prevHoverCell modules.EventCell

	w, h := win.Size()
	pw, ph := 0, 0
	mouseX, _ := 0, 0
	utils.Logger.Printf("Panel Size (cells): %d, %d\n", w, h)
	mouseShape := vaxis.MouseShapeDefault

	tui.Init(w, h, l, m, r, cfg.Bar)
	tui.FullRender(win)
	vx.Render()

	isRunning := true
	for isRunning {
		select {
		case ev := <-screenEvents:
			switch ev := ev.(type) {
			case vaxis.Resize:
				pw, ph = ev.XPixel, ev.YPixel
				win = vx.Window()
				w, h = win.Size()
				tui.Resize(w, h)
				tui.FullRender(win)
				vx.Render()
				utils.Logger.Printf("Panel Size: %d, %d\n", pw, ph)
			case vaxis.Redraw:
				tui.FullRender(win)
				vx.Render()
			case vaxis.Key:
				if ev.String() == "Ctrl+c" {
					isRunning = false
					vx.PostEvent(vaxis.QuitEvent{})
				}

			case vaxis.FocusOut:
				if prevHoverMod != nil {
					_, send := prevHoverMod.Channels()
					send <- modules.Event{
						Cell: prevHoverCell,
						VaxisEvent: modules.FocusOut{
							PrevMod: prevHoverMod,
							NewMod:  nil,
						},
					}
					prevHoverMod = nil
				}
			case vaxis.Mouse:
				mouseX, _ = ev.Col, ev.Row

				if ev.EventType == vaxis.EventLeave {
					vx.PostEvent(vaxis.FocusOut{})
					continue
				}
				c := tui.State()[mouseX]

				curMod := c.Mod
				if curMod != prevHoverMod {
					if prevHoverMod != nil {
						_, send := prevHoverMod.Channels()
						send <- modules.Event{
							Cell: prevHoverCell,
							VaxisEvent: modules.FocusOut{
								PrevMod: prevHoverMod,
								NewMod:  curMod,
							},
						}
					}

					if curMod != nil {
						_, send := curMod.Channels()
						send <- modules.Event{
							Cell: c,
							VaxisEvent: modules.FocusIn{
								PrevMod: prevHoverMod,
								NewMod:  curMod,
							},
						}
					}
					prevHoverMod = curMod
					prevHoverCell = c
				}

				if curMod != nil {
					_, send := curMod.Channels()
					send <- modules.Event{Cell: c, VaxisEvent: ev}
				}
				updateMouseShape(vx, c, &mouseShape, true)
			case vaxis.QuitEvent:
				utils.Logger.Printf("Received exit signal\n")
				isRunning = false
			}
		case m := <-modev:
			utils.Logger.Println("render:", m.Name())
			tui.PartialRender(win, m)
			vx.Render()
		case s := <-userSignals:
			utils.Logger.Printf("full render: %s", canonicalSignalName(s))
			win = vx.Window()
			w, h = win.Size()
			tui.Resize(w, h)
			tui.FullRender(win)
			vx.Render()
		case <-resumeCh:
			utils.Logger.Printf("full render: waking from suspend")
			win = vx.Window()
			w, h = win.Size()
			tui.Resize(w, h)
			tui.FullRender(win)
			vx.Render()
		}
	}
	return 0
}

func updateMouseShape(
	vx *vaxis.Vaxis,
	ec modules.EventCell,
	old *vaxis.MouseShape,
	render bool,
) {
	target := ec.MouseShape
	if target == "" {
		target = vaxis.MouseShapeDefault
	}
	if ec.Mod == nil {
		target = vaxis.MouseShapeDefault
	}

	if *old == target {
		return
	}

	*old = target
	utils.Logger.Printf("mouse shape: %s\n", *old)
	vx.SetMouseShape(target)

	if render {
		vx.Render()
	}
}
