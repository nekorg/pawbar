package calendar

import (
	"github.com/nekorg/pawbar/internal/menus/calendar/tui"
	"github.com/nekorg/pawbar/pkg/menus"
)

// Spec is the calendar menu: a custom-drawn month view.
func Spec() menus.Spec {
	return menus.Spec{Name: "calendar", Width: 21, Height: 8}
}

func init() {
	menus.Register("calendar", tui.App)
}
