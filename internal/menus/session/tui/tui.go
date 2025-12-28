package tui

import (
	"fmt"
	"io"
	"os"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/godbus/dbus/v5"
	"github.com/nekorg/katnip"
)

func Panel(k *katnip.Kitty, rw io.ReadWriter) int {
	vx, err := vaxis.New(vaxis.Options{
		WithTTY:         os.Stdout.Name(),
		EnableSGRPixels: true,
	})
	if err != nil {
		return 1
	}
	defer vx.Close()
	win := vx.Window()

	conn, obj, err := connect()
	if err != nil {
		fmt.Errorf("error in connection: %s", err)
	}
	defer conn.Close()

	draw(-1, win)
	vx.Render()
	sel := -1
	redraw := func() {
		draw(sel, win)
		vx.Render()
	}

	for ev := range vx.Events() {
		switch ev := ev.(type) {
		case vaxis.Key:
			if ev.EventType == vaxis.EventPress {
				switch ev.Keycode {
				case vaxis.KeyEsc:
					return 0
				case vaxis.KeyUp:
				case vaxis.KeyDown:
				}
			}
		case vaxis.Mouse:
			newSel := -1
			if ev.Row >= 0 && ev.Row < 4 {
				newSel = ev.Row
			}
			if newSel != sel {
				sel = newSel
				redraw()
			}
			if ev.EventType == vaxis.EventLeave {
				sel = -1
				redraw()
			}

			if ev.Button == vaxis.MouseLeftButton && ev.EventType == vaxis.EventPress {
				switch sel {
				case 0:
					setProfile("reboot", obj)
				case 1:
					setProfile("poweroff", obj)
				case 2:
					setProfile("suspend", obj)
				case 3:
					callTerminate(obj)
				}
				return 0
			}
		}
	}
	return 0
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

func callTerminate(obj dbus.BusObject) {
	sess_id := os.Getenv("XDG_SESSION_ID")
	if sess_id == "" {
		return
	}
	call := obj.Call("org.freedesktop.login1.Manager.TerminateSession", 0, sess_id)
	if call.Err != nil {
		fmt.Errorf("error in setting session control: %s", call.Err)
	}
	return
}

func setProfile(session string, obj dbus.BusObject) {
	var call *dbus.Call
	switch session {
	case "reboot":
		call = obj.Call("org.freedesktop.login1.Manager.Reboot", 0, true)
	case "poweroff":
		call = obj.Call("org.freedesktop.login1.Manager.PowerOff", 0, true)
	case "suspend":
		call = obj.Call("org.freedesktop.login1.Manager.Suspend", 0, true)
	}
	if call.Err != nil {
		fmt.Errorf("error in setting session control: %s", call.Err)
	}
	return
}

func draw(sel int, win vaxis.Window) {
	win.Clear()

	normal := vaxis.Style{Foreground: vaxis.ColorWhite}
	highlighted := vaxis.Style{
		Foreground: vaxis.ColorWhite,
		Background: vaxis.ColorGray,
	}

	items := []string{" 󰜉 Reboot", "  Poweroff", " 󰤄 Suspend", " 󰍃 Logout"}
	width, _ := win.Size()
	for i, text := range items {
		style := normal
		if i == sel {
			style = highlighted
		}
		line := fmt.Sprintf("%-*s", width, text)
		win.Println(i, vaxis.Segment{
			Text:  line,
			Style: style,
		})
	}
}
