// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package mpris

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/pkg/module"
)

//go:embed mpris.yaml
var defaults []byte

func init() {
	module.Register(module.Def{
		Name: "mpris",
		Doc:  "media player status via MPRIS",
		New:  func() module.Module { return &mprisModule{} },
		States: []module.StateDef{
			{Name: "playing", Doc: "a player is playing"},
			{Name: "paused", Doc: "a player is paused"},
		},
		Placeholders: []module.Placeholder{
			{Name: "artists", Doc: "track artists, comma separated", Kind: module.KindString},
			{Name: "title", Doc: "track title", Kind: module.KindString},
		},
		Verbs: []module.VerbDef{
			{Name: "play-pause", Doc: "toggle playback on the active player"},
		},
		Defaults: defaults,
	})
}

type mprisModule struct {
	conn    *dbus.Conn
	artists []string
	title   string
}

func (m *mprisModule) Init(ctx *module.Ctx) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	m.conn = conn

	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0,
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'")
	if call.Err != nil {
		return call.Err
	}
	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)

	// No player yet is fine; the first PropertiesChanged fills us in.
	m.initState(ctx)

	module.On(ctx, module.Chan(ch), func(sig *dbus.Signal) { m.handleSignal(ctx, sig) })

	ctx.HandleVerb("play-pause", func(module.VerbArgs) error {
		player, err := m.activePlayer()
		if err != nil {
			return err
		}
		obj := m.conn.Object(player, dbus.ObjectPath("/org/mpris/MediaPlayer2"))
		if call := obj.Call("org.mpris.MediaPlayer2.Player.PlayPause", 0); call.Err != nil {
			return fmt.Errorf("PlayPause on %s: %w", player, call.Err)
		}
		return nil
	})
	return nil
}

func (m *mprisModule) Stop(ctx *module.Ctx) {
	if m.conn != nil {
		m.conn.Close()
	}
}

func (m *mprisModule) activePlayer() (string, error) {
	var busNames []string
	if err := m.conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&busNames); err != nil {
		return "", fmt.Errorf("list bus names: %w", err)
	}

	var candidate string
	for _, name := range busNames {
		if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
			continue
		}
		obj := m.conn.Object(name, dbus.ObjectPath("/org/mpris/MediaPlayer2"))
		variant, err := obj.GetProperty("org.mpris.MediaPlayer2.Player.PlaybackStatus")
		if err != nil {
			if candidate == "" {
				candidate = name
			}
			continue
		}
		if status, ok := variant.Value().(string); ok && (status == "Playing" || status == "Paused") {
			return name, nil
		}
		if candidate == "" {
			candidate = name
		}
	}
	if candidate == "" {
		return "", fmt.Errorf("no MPRIS player found on the session bus")
	}
	return candidate, nil
}

func (m *mprisModule) initState(ctx *module.Ctx) {
	player, err := m.activePlayer()
	if err != nil {
		return
	}
	obj := m.conn.Object(player, dbus.ObjectPath("/org/mpris/MediaPlayer2"))
	if variant, err := obj.GetProperty("org.mpris.MediaPlayer2.Player.PlaybackStatus"); err == nil {
		if status, ok := variant.Value().(string); ok {
			m.setPlayback(ctx, status)
		}
	}
	if variant, err := obj.GetProperty("org.mpris.MediaPlayer2.Player.Metadata"); err == nil {
		if metaMap, ok := variant.Value().(map[string]dbus.Variant); ok {
			m.applyMetadata(metaMap)
		}
	}
}

func (m *mprisModule) handleSignal(ctx *module.Ctx, sig *dbus.Signal) {
	if len(sig.Body) < 3 {
		return
	}
	iface, ok := sig.Body[0].(string)
	if !ok || iface != "org.mpris.MediaPlayer2.Player" {
		return
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return
	}
	for prop, val := range changed {
		switch prop {
		case "PlaybackStatus":
			if status, ok := val.Value().(string); ok {
				m.setPlayback(ctx, status)
			}
		case "Metadata":
			if metaMap, ok := val.Value().(map[string]dbus.Variant); ok {
				m.applyMetadata(metaMap)
			}
		}
	}
}

func (m *mprisModule) setPlayback(ctx *module.Ctx, status string) {
	ctx.SetState("playing", status == "Playing")
	ctx.SetState("paused", status == "Paused")
}

func (m *mprisModule) applyMetadata(metaMap map[string]dbus.Variant) {
	if titleVar, found := metaMap["xesam:title"]; found {
		if title, ok := titleVar.Value().(string); ok {
			m.title = title
		}
	}
	if artistVar, found := metaMap["xesam:artist"]; found {
		if artists, ok := artistVar.Value().([]string); ok && len(artists) > 0 {
			m.artists = artists
		}
	}
}

func (m *mprisModule) Render(w *module.Writer) {
	w.Text(module.P{
		"artists": strings.Join(m.artists, ","),
		"title":   m.title,
	})
}
