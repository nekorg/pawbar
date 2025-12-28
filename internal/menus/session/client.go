package session

import (
	"github.com/nekorg/katnip"
	"github.com/nekorg/pawbar/internal/menus/session/tui"
	"github.com/nekorg/pawbar/internal/utils"
)

func LaunchMenu(x, y int) {
	kn := CreatePanel(x, y, 12, 4)
	kn.Wait()
}

func init() {
	katnip.RegisterFunc("session", tui.Panel)
}

func CreatePanel(x, y, w, h int) *katnip.Panel {
	conf := katnip.Config{
		Position: katnip.Vector{X: x, Y: y},
		Size:     katnip.Vector{X: w, Y: h},
		Edge:     katnip.EdgeNone,
		Layer:    katnip.LayerTop,
		// FocusPolicy: katnip.FocusNotAllowed,
		FocusPolicy: katnip.FocusExclusive,
		ConfigFile:  "NONE",
		KittyOverrides: []string{
			"font_size=12",
			"cursor_trail=0",
			"cursor_shape=beam",
			"cursor=#000000",
			"paste_actions=replace-dangerous-control-codes",
			"map kitty_mod+equal       no_op",
			"map kitty_mod+plus        no_op",
			"map kitty_mod+kp_add      no_op",
			"map cmd+plus              no_op",
			"map cmd+equal             no_op",
			"map shift+cmd+equal       no_op",
			"map kitty_mod+minus       no_op",
			"map kitty_mod+kp_subtract no_op",
			"map cmd+minus             no_op",
			"map shift+cmd+minus       no_op",
			"map kitty_mod+backspace   no_op",
			"map cmd+0                 no_op",
		},
	}

	kn := katnip.NewPanel("session", conf)
	utils.Logger.Printf(kn.Cmd.String())
	kn.Start()

	return kn
}
