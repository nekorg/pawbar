package power

import (
	"github.com/nekorg/pawbar/pkg/menus"
)

// Menu builds the power-profile picker. active is the currently active
// profile name; set applies a new one (the caller's own bus connection
// and state subscription keep the bar in sync afterwards).
func Menu(active string, set func(profile string) error) *menus.List {
	mk := func(label, profile string) menus.Item {
		return menus.Item{
			Label:   label,
			Toggle:  menus.ToggleRadio,
			Checked: active == profile,
			OnClick: func() { set(profile) },
		}
	}
	return &menus.List{
		Items: []menus.Item{
			mk("Performance", "performance"),
			mk("Balanced", "balanced"),
			mk("Power-saving", "power-saver"),
		},
	}
}
