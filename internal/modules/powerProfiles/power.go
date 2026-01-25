package powerprofiles

import (
	"bytes"
	"fmt"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/godbus/dbus/v5"

	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/menus/power"
	"github.com/nekorg/pawbar/internal/modules"
	"github.com/nekorg/pawbar/internal/utils"
)

type powerProfileModule struct {
	receive     chan bool
	send        chan modules.Event
	state       string
	opts        Options
	initialOpts Options
}

func (mod *powerProfileModule) Dependencies() []string {
	return nil
}

func (mod *powerProfileModule) Channels() (<-chan bool, chan<- modules.Event) {
	return mod.receive, mod.send
}

func (mod *powerProfileModule) Name() string {
	return "powerprofiles"
}

func connect() (*dbus.Conn, dbus.BusObject, chan *dbus.Signal, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to connect system bus: %s", err)
	}

	obj := conn.Object(
		"org.freedesktop.UPower.PowerProfiles",
		"/org/freedesktop/UPower/PowerProfiles",
	)

	rule := "type='signal',sender='org.freedesktop.UPower.PowerProfiles',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'"
	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)
	if call.Err != nil {
		return conn, obj, nil, fmt.Errorf("Failed to add match rule: %s", call.Err)
	}

	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)
	return conn, obj, ch, nil
}

func getProfile(obj dbus.BusObject) string {
	var v dbus.Variant
	err := obj.Call("org.freedesktop.DBus.Properties.Get", 0,
		"org.freedesktop.UPower.PowerProfiles",
		"ActiveProfile",
	).Store(&v)
	if err != nil {
		utils.Logger.Printf("error in getting ActiveProfile: %s", err)
	}
	return v.Value().(string)
}

func (mod *powerProfileModule) Run() (<-chan bool, chan<- modules.Event, error) {
	mod.receive = make(chan bool)
	mod.send = make(chan modules.Event)
	mod.initialOpts = mod.opts

	_, obj, ch, err := connect()
	if err != nil {
		utils.Logger.Printf("error in connection: %s", err)
		return nil, nil, err
	}

	mod.state = getProfile(obj)

	go func() {
		for {
			select {
			case e := <-mod.send:
				switch ev := e.VaxisEvent.(type) {
				case vaxis.Mouse:
					if ev.EventType != vaxis.EventPress {
						break
					}
					btn := config.ButtonName(ev)

					if mod.opts.OnClick.Dispatch(btn, &mod.initialOpts, &mod.opts) {
						mod.receive <- true
					}

					switch ev.Button {
					case vaxis.MouseRightButton:
						go power.LaunchMenu(ev.XPixel/2, ev.YPixel/2)
					}

				case modules.FocusIn:
					if mod.opts.OnClick.HoverIn(&mod.opts) {
						mod.receive <- true
					}

				case modules.FocusOut:
					if mod.opts.OnClick.HoverOut(&mod.opts) {
						mod.receive <- true
					}
				}
			case signal := <-ch:
				iface, ok := signal.Body[0].(string)
				changed, ok := signal.Body[1].(map[string]dbus.Variant)
				if !ok {
					continue
				}

				switch iface {
				case "org.freedesktop.UPower.PowerProfiles":
					if v, ok := changed["ActiveProfile"]; ok {
						s, _ := v.Value().(string)
						if s == "performance" {
							mod.state = "performance"
						} else if s == "balanced" {
							mod.state = "balanced"
						} else if s == "power-saver" {
							mod.state = "power-saver"
						}
						mod.receive <- true

					}
				}
			}
		}
	}()
	return mod.receive, mod.send, nil
}

func (mod *powerProfileModule) Render() []modules.EventCell {
	icon := "󰐦"

	switch mod.state {
	case "performance":
		icon = "󰓅"
	case "balanced":
		icon = "󰾅"
	case "power-saver":
		icon = "󰾆"
	}

	data := struct {
		Icon string
	}{
		Icon: string(icon),
	}

	var s vaxis.Style
	s.Foreground = mod.opts.Fg.Go()
	s.Background = mod.opts.Bg.Go()

	var buf bytes.Buffer
	_ = mod.opts.Format.Execute(&buf, data)

	chars := vaxis.Characters(buf.String())
	r := make([]modules.EventCell, len(chars))

	for i, ch := range chars {
		r[i] = modules.EventCell{
			C: vaxis.Cell{
				Character: ch,
				Style:     s,
			},
			Metadata:   "",
			Mod:        mod,
			MouseShape: mod.opts.Cursor.Go(),
		}
	}
	return r
}
