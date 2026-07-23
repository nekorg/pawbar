// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package menus

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/codelif/gorsvg"
	"github.com/codelif/xdgicons/missing"
	"github.com/nekorg/pawbar/internal/logging"
	"github.com/nekorg/pawbar/pkg/menus/wire"
	"go.rockorager.dev/vaxis"
	"golang.org/x/image/colornames"
)

// The canonical list-menu row: a fixed-width gutter holding the item's
// toggle mark, glyph or icon (centered), then the label, then a right
// pad whose last cell holds the submenu arrow. Labels always start at
// column gutterCells, in every menu.
const (
	iconSize       = 32
	iconCellWidth  = 2
	iconCellHeight = 1
	gutterCells    = 3
	rightPadCells  = 2
	iconInset      = 2 // pixels of breathing room around a gutter icon
)

const submenuArrow = "►"

// listRenderer draws one list panel's items.
type listRenderer struct {
	win vaxis.Window
	fg  color.Color
	// pixels per cell, for pixel-precise icon placement; 0 when the
	// terminal didn't report pixel sizes.
	cellW, cellH int
	// icons caches encoded images per item: KittyImage encodes
	// asynchronously (Draw no-ops until ready, then a Redraw event
	// fires), so re-draws must place the same image, not encode a new
	// one.
	icons map[int32]*vaxis.KittyImage
}

func newListRenderer(win vaxis.Window, fg color.Color) *listRenderer {
	r := &listRenderer{win: win, fg: fg, icons: make(map[int32]*vaxis.KittyImage)}
	if size := win.Vx.Size(); size.Cols > 0 && size.Rows > 0 {
		r.cellW = size.XPixel / size.Cols
		r.cellH = size.YPixel / size.Rows
	}
	return r
}

func (r *listRenderer) draw(st *listState, showIcons bool) {
	for i := range st.items {
		it := &st.items[i]
		if it.Separator {
			r.drawSeparator(i)
		} else {
			r.drawItem(it, i, st, showIcons)
		}
	}
}

func (r *listRenderer) drawSeparator(row int) {
	w, _ := r.win.Size()
	r.win.Println(row, vaxis.Segment{
		Text:  strings.Repeat("─", w),
		Style: vaxis.Style{Attribute: vaxis.AttrDim},
	})
}

func (r *listRenderer) itemStyle(it *wire.Item, row int, st *listState) vaxis.Style {
	var style vaxis.Style
	if row == st.row {
		if st.mousePressed {
			style.Background = vaxis.ColorBlue
		} else {
			style.Background = vaxis.ColorGray
		}
	}
	if it.Disabled {
		style.Background = 0
		style.Attribute |= vaxis.AttrDim
	}
	return style
}

func (r *listRenderer) drawItem(it *wire.Item, row int, st *listState, showIcons bool) {
	style := r.itemStyle(it, row, st)
	iconColor := r.fg
	if it.Disabled {
		iconColor = colornames.Gray
	}

	// Background first so the highlight spans the full row.
	w, _ := r.win.Size()
	r.win.Println(row, vaxis.Segment{Text: strings.Repeat(" ", w), Style: style})

	r.win.Println(row, vaxis.Segment{Text: gutterText(it) + it.Label, Style: style})

	if it.HasSubmenu && w >= 1 {
		r.win.New(w-1, row, 1, 1).Println(0, vaxis.Segment{Text: submenuArrow, Style: style})
	}

	if showIcons && gutterMark(it) == "" &&
		(it.IconData != nil || it.IconPath != "" || it.IconName != "") {
		r.renderIcon(it, row, iconColor)
	}
}

// gutterMark returns the item's textual gutter content, if any.
func gutterMark(it *wire.Item) string {
	switch it.Toggle {
	case wire.ToggleCheck:
		if it.Checked {
			return "✓"
		}
		return ""
	case wire.ToggleRadio:
		if it.Checked {
			return "●"
		}
		return "○"
	}
	return it.Glyph
}

// gutterText renders the gutter as exactly gutterCells columns with
// the mark centered (extra space goes to the label side).
func gutterText(it *wire.Item) string {
	mark := gutterMark(it)
	if mark == "" {
		return strings.Repeat(" ", gutterCells)
	}
	// Marks and glyphs are single-column characters.
	left := gutterCells / 2
	return strings.Repeat(" ", left) + mark + strings.Repeat(" ", gutterCells-left-1)
}

func (r *listRenderer) renderIcon(it *wire.Item, row int, defaultColor color.Color) {
	kimg, cached := r.icons[it.ID]

	var img image.Image
	if !cached {
		var err error
		switch {
		case it.IconData != nil:
			img, err = png.Decode(bytes.NewReader(it.IconData))
			if err != nil {
				img = missing.GenerateMissingIconBroken(iconSize, defaultColor)
			}
		case it.IconPath != "":
			img, err = loadIcon(it.IconPath, it.IconName, defaultColor)
			if err != nil {
				img = missing.GenerateMissingIcon(iconSize, defaultColor)
			}
		default:
			return
		}
	}

	if r.cellW > 0 && r.cellH > 0 {
		// Scale the icon to fit one row (with a small inset) and
		// center it pixel-precisely across the gutter. The absolute
		// offset splits into an anchor cell plus an intra-cell rest,
		// because the kitty X placement key must stay below one cell.
		gutterW := gutterCells * r.cellW
		if !cached {
			kimg = r.win.Vx.NewKittyGraphic(img)
			kimg.ResizePixels(gutterW-2*iconInset, r.cellH-2*iconInset)
			r.icons[it.ID] = kimg
		}
		pw, ph := kimg.PixelSize()
		ox := (gutterW - pw) / 2
		col := ox / r.cellW
		xOff := ox % r.cellW
		yOff := (r.cellH - ph) / 2
		kimg.SetOffset(xOff, yOff)
		logging.Log.Debug().Msgf(
			"menus: icon cell=%dx%d px=%dx%d gutter=%d col=%d off=(%d,%d)",
			r.cellW, r.cellH, pw, ph, gutterW, col, xOff, yOff)
		kimg.Draw(r.win.New(col, row, gutterCells-col, iconCellHeight))
		return
	}

	// No pixel metrics: fall back to cell-level placement, biased
	// toward the label side when the leftover space is odd.
	if !cached {
		kimg = r.win.Vx.NewKittyGraphic(img)
		kimg.Resize(iconCellWidth, iconCellHeight)
		r.icons[it.ID] = kimg
	}
	iw, ih := kimg.CellSize()
	x := 0
	if iw < gutterCells {
		x = (gutterCells - iw + 1) / 2
	}
	kimg.Draw(r.win.New(x, row, iw, ih))
}

func loadIcon(path, name string, c color.Color) (image.Image, error) {
	isSymbolic := strings.HasSuffix(name, "-symbolic")
	ext := strings.ToLower(filepath.Ext(path))

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening file %s: %w", path, err)
	}
	defer f.Close()

	switch ext {
	case ".svg":
		if isSymbolic {
			return gorsvg.DecodeWithColor(f, 48, 48, c)
		}
		return gorsvg.Decode(f, 48, 48)
	case ".png":
		return png.Decode(f)
	default:
		return nil, fmt.Errorf("unsupported image format: %s", ext)
	}
}
