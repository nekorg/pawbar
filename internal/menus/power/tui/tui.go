package tui

import (
	"fmt"
	"io"
	"os"
	"time"

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
		fmt.Fprintf(os.Stderr, "error in connection: %s\n", err)
	}
	defer conn.Close()

	draw(-1, win, getProfile(obj))
	vx.Render()
	sel := -1
	redraw := func() {
		draw(sel, win, getProfile(obj))
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
			if ev.Row >= 0 && ev.Row < 3 {
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
					setProfile("performance", obj)
					redraw()
				case 1:
					setProfile("balanced", obj)
					redraw()
				case 2:
					setProfile("power-saver", obj)
					redraw()
				}
				time.Sleep(300 * time.Millisecond)
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
		"org.freedesktop.UPower.PowerProfiles",
		"/org/freedesktop/UPower/PowerProfiles",
	)
	return conn, obj, nil
}

func setProfile(profile string, obj dbus.BusObject) {
	call := obj.Call("org.freedesktop.DBus.Properties.Set", 0,
		"org.freedesktop.UPower.PowerProfiles",
		"ActiveProfile",
		dbus.MakeVariant(profile),
	)
	if call.Err != nil {
		fmt.Fprintf(os.Stderr, "error in setting ActiveProfile: %s\n", call.Err)
	}
}

func getProfile(obj dbus.BusObject) string {
	var v dbus.Variant
	err := obj.Call("org.freedesktop.DBus.Properties.Get", 0,
		"org.freedesktop.UPower.PowerProfiles",
		"ActiveProfile",
	).Store(&v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error in getting ActiveProfile: %s\n", err)
	}
	return v.Value().(string)
}

func draw(sel int, win vaxis.Window, mode string) {
	win.Clear()

	normal := vaxis.Style{Foreground: vaxis.ColorWhite}
	highlighted := vaxis.Style{
		Foreground: vaxis.ColorWhite,
		Background: vaxis.ColorGray,
	}

	items := []string{"Performance", "Balanced", "Power-saving"}
	modeIndex := map[string]int{
		"performance": 0,
		"balanced":    1,
		"power-saver": 2,
	}

	active := -1
	if idx, ok := modeIndex[mode]; ok {
		active = idx
	}

	width, _ := win.Size()
	for i, text := range items {
		style := normal
		prefix := "   "
		if i == sel {
			style = highlighted
		}
		if i == active {
			prefix = "  "
		}
		line := fmt.Sprintf("%-*s", width, prefix+text)
		win.Println(i, vaxis.Segment{
			Text:  line,
			Style: style,
		})
	}
}
