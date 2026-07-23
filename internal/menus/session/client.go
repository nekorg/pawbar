package session

import (
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus"
)

// Menu builds the session-controls menu (reboot, poweroff, suspend,
// logout). Actions go through logind on the system bus; the connection
// lives for the menu's lifetime.
func Menu() *menus.List {
	conn, obj, err := connect()
	if err != nil {
		logging.Log.Error().Msgf("session menu: %v", err)
		return &menus.List{Items: []menus.Item{
			{Label: "logind unavailable", Disabled: true},
		}}
	}

	call := func(fn func(dbus.BusObject)) func() {
		return func() { fn(obj) }
	}
	return &menus.List{
		Items: []menus.Item{
			{Glyph: "󰜉", Label: "Reboot", OnClick: call(reboot)},
			{Glyph: "", Label: "Poweroff", OnClick: call(poweroff)},
			{Glyph: "󰤄", Label: "Suspend", OnClick: call(suspend)},
			{Glyph: "󰍃", Label: "Logout", OnClick: call(terminateSession)},
		},
		OnClose: func() { conn.Close() },
	}
}

func connect() (*dbus.Conn, dbus.BusObject, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect system bus: %w", err)
	}
	obj := conn.Object(
		"org.freedesktop.login1",
		"/org/freedesktop/login1",
	)
	return conn, obj, nil
}

func reboot(obj dbus.BusObject) {
	logCall(obj.Call("org.freedesktop.login1.Manager.Reboot", 0, true))
}

func poweroff(obj dbus.BusObject) {
	logCall(obj.Call("org.freedesktop.login1.Manager.PowerOff", 0, true))
}

func suspend(obj dbus.BusObject) {
	logCall(obj.Call("org.freedesktop.login1.Manager.Suspend", 0, true))
}

func terminateSession(obj dbus.BusObject) {
	sessID := os.Getenv("XDG_SESSION_ID")
	if sessID == "" {
		return
	}
	logCall(obj.Call("org.freedesktop.login1.Manager.TerminateSession", 0, sessID))
}

func logCall(call *dbus.Call) {
	if call.Err != nil {
		logging.Log.Error().Msgf("session menu: %v", call.Err)
	}
}
