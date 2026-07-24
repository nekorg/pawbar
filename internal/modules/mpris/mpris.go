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

const (
	mprisPrefix = "org.mpris.MediaPlayer2."
	mprisPath   = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	playerIface = "org.mpris.MediaPlayer2.Player"
)

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
			{Name: "raise", Doc: "bring the active player's user interface to the front"},
		},
		Defaults: defaults,
	})
}

type mprisModule struct {
	conn *dbus.Conn

	// player is the well-known bus name being tracked; owner is its
	// unique name, which is what signal senders carry.
	player  string
	owner   string
	artists []string
	title   string
}

func (m *mprisModule) Init(ctx *module.Ctx) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	m.conn = conn

	// Player property changes, narrowed to the MPRIS object path;
	// handleSignal still has to check the sender against the tracked
	// player.
	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0,
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='"+string(mprisPath)+"'")
	if call.Err != nil {
		return call.Err
	}
	// Player lifecycle: names appearing/disappearing under the MPRIS
	// namespace.
	call = conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0,
		"type='signal',sender='org.freedesktop.DBus',interface='org.freedesktop.DBus',member='NameOwnerChanged',arg0namespace='org.mpris.MediaPlayer2'")
	if call.Err != nil {
		return call.Err
	}
	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)

	// No player yet is fine; NameOwnerChanged adopts the first one.
	m.reselect(ctx)

	module.On(ctx, module.Chan(ch), func(sig *dbus.Signal) { m.handleSignal(ctx, sig) })

	ctx.HandleVerb("play-pause", func(module.VerbArgs) error {
		obj, player, err := m.resolvePlayer()
		if err != nil {
			return err
		}
		if call := obj.Call(playerIface+".PlayPause", 0); call.Err != nil {
			return fmt.Errorf("PlayPause on %s: %w", player, call.Err)
		}
		return nil
	})

	ctx.HandleVerb("raise", func(module.VerbArgs) error {
		obj, player, err := m.resolvePlayer()
		if err != nil {
			return err
		}
		variant, err := obj.GetProperty(playerIface + ".CanRaise")
		if err != nil {
			return fmt.Errorf("CanRaise on %s: %w", player, err)
		}
		if canRaise, _ := variant.Value().(bool); !canRaise {
			ctx.Log("raise: %s cannot be raised (CanRaise=false)", player)
			return nil
		}
		if call := obj.Call(playerIface+".Raise", 0); call.Err != nil {
			return fmt.Errorf("Raise on %s: %w", player, call.Err)
		}
		return nil
	})
	return nil
}

// resolvePlayer returns the D-Bus object and bus name a verb should act
// on: the tracked player, or the active one when none is tracked yet.
func (m *mprisModule) resolvePlayer() (dbus.BusObject, string, error) {
	player := m.player
	if player == "" {
		var err error
		if player, err = m.activePlayer(); err != nil {
			return nil, "", err
		}
	}
	return m.conn.Object(player, mprisPath), player, nil
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
		if !strings.HasPrefix(name, mprisPrefix) {
			continue
		}
		obj := m.conn.Object(name, mprisPath)
		variant, err := obj.GetProperty(playerIface + ".PlaybackStatus")
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

// reselect picks the most relevant player and adopts its state, or
// clears everything when no player is left.
func (m *mprisModule) reselect(ctx *module.Ctx) {
	player, err := m.activePlayer()
	if err != nil {
		m.clear(ctx)
		return
	}
	m.adopt(ctx, player)
}

// adopt starts tracking player and loads its current state.
func (m *mprisModule) adopt(ctx *module.Ctx, player string) {
	m.player = player
	m.owner = ""
	var owner string
	if err := m.conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, player).Store(&owner); err == nil {
		m.owner = owner
	}

	m.title, m.artists = "", nil
	obj := m.conn.Object(player, mprisPath)
	if variant, err := obj.GetProperty(playerIface + ".PlaybackStatus"); err == nil {
		if status, ok := variant.Value().(string); ok {
			m.setPlayback(ctx, status)
		}
	}
	if variant, err := obj.GetProperty(playerIface + ".Metadata"); err == nil {
		if metaMap, ok := variant.Value().(map[string]dbus.Variant); ok {
			m.applyMetadata(metaMap)
		}
	}
}

func (m *mprisModule) clear(ctx *module.Ctx) {
	m.player, m.owner = "", ""
	m.title, m.artists = "", nil
	ctx.SetState("playing", false)
	ctx.SetState("paused", false)
}

func (m *mprisModule) handleSignal(ctx *module.Ctx, sig *dbus.Signal) {
	switch sig.Name {
	case "org.freedesktop.DBus.NameOwnerChanged":
		if len(sig.Body) < 3 {
			return
		}
		name, _ := sig.Body[0].(string)
		newOwner, _ := sig.Body[2].(string)
		if !strings.HasPrefix(name, mprisPrefix) {
			return
		}
		switch {
		case name == m.player && newOwner == "":
			// Our player exited; fall back to whatever is left.
			m.reselect(ctx)
		case name == m.player:
			m.owner = newOwner
		case m.player == "" && newOwner != "":
			m.adopt(ctx, name)
		}

	case "org.freedesktop.DBus.Properties.PropertiesChanged":
		if len(sig.Body) < 3 {
			return
		}
		iface, ok := sig.Body[0].(string)
		if !ok || iface != playerIface {
			return
		}
		changed, ok := sig.Body[1].(map[string]dbus.Variant)
		if !ok {
			return
		}

		if sig.Sender != m.owner {
			// Another player: switch to it only when it starts
			// playing, so background players can't clobber the
			// tracked one.
			if status, ok := stringProp(changed, "PlaybackStatus"); ok && status == "Playing" {
				m.reselect(ctx)
			}
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
}

func stringProp(props map[string]dbus.Variant, key string) (string, bool) {
	v, ok := props[key]
	if !ok {
		return "", false
	}
	s, ok := v.Value().(string)
	return s, ok
}

func (m *mprisModule) setPlayback(ctx *module.Ctx, status string) {
	ctx.SetState("playing", status == "Playing")
	ctx.SetState("paused", status == "Paused")
}

// applyMetadata replaces the track info: MPRIS Metadata is a full
// snapshot, so a missing key means the new track has no such field.
func (m *mprisModule) applyMetadata(metaMap map[string]dbus.Variant) {
	m.title, m.artists = "", nil
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
