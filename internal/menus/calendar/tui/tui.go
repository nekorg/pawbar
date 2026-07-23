package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nekorg/pawbar/pkg/menus"
	"go.rockorager.dev/vaxis"
)

var (
	now       = time.Now()
	currYear  = now.Year()
	currMonth = now.Month()
)

func inc() {
	if currMonth == time.December {
		currYear += 1
		currMonth = time.January
	} else {
		currMonth = currMonth + 1
	}
}

func dec() {
	if currMonth == time.January {
		currYear -= 1
		currMonth = time.December
	} else {
		currMonth = currMonth - 1
	}
}

func App(s *menus.Session) int {
	draw := func() {
		win := s.Window()
		win.Clear()
		printMonthCal(win, currYear, currMonth)
		s.Render()
	}
	draw()

	for ev := range s.Events() {
		switch ev := ev.(type) {
		case vaxis.Key:
			if ev.EventType == vaxis.EventPress {
				switch ev.Keycode {
				case vaxis.KeyLeft, vaxis.KeyUp:
					dec()
					draw()
				case vaxis.KeyRight, vaxis.KeyDown:
					inc()
					draw()
				}
			}
		case vaxis.Mouse:
			switch ev.Button {
			case vaxis.MouseWheelDown:
				inc()
				draw()
			case vaxis.MouseWheelUp:
				dec()
				draw()
			}
		case vaxis.Resize, vaxis.Redraw:
			draw()
		}
	}
	return 0
}

func printMonthCal(win vaxis.Window, year int, month time.Month) {
	const width = 20
	title := fmt.Sprintf("%s %d", month, year)
	win.Println(0, vaxis.Segment{Text: center(title, width)})
	win.Println(1, vaxis.Segment{Text: "Su Mo Tu We Th Fr Sa"})

	loc := time.Now().Location()
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	offset := int(first.Weekday())
	daysInMonth := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	day := 1
	for week := 0; week < 6; week++ {
		var segs []vaxis.Segment
		for weekday := 0; weekday < 7; weekday++ {
			cellIndex := week*7 + weekday
			if cellIndex < offset || day > daysInMonth {
				segs = append(segs, vaxis.Segment{Text: "   "})
				continue
			}
			var style vaxis.Style
			if day == now.Day() && month == now.Month() && year == now.Year() {
				style.Attribute |= vaxis.AttrReverse
			} else if day == now.Day() {
				style.Background = vaxis.IndexColor(243)
				style.Foreground = vaxis.IndexColor(15)
			}
			segs = append(segs,
				vaxis.Segment{Text: fmt.Sprintf("%2d", day), Style: style},
				vaxis.Segment{Text: " "},
			)
			day++
		}
		win.Println(2+week, segs...)
	}
}

func center(s string, width int) string {
	if len(s) >= width {
		return s
	}
	left := (width - len(s)) / 2
	return strings.Repeat(" ", left) + s
}
