// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package tray

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/codelif/gorsvg"
	"github.com/codelif/xdgicons"
	"github.com/nekorg/pawbar/internal/services/sni"
	"github.com/nekorg/pawbar/pkg/module"
)

// iconCells is how many bar columns a rendered tray icon spans.
const iconCells = 2

var iconLookup = xdgicons.NewIconLookupWithConfig(xdgicons.LookupConfig{FallbackTheme: "Adwaita"})

// iconColor is the color symbolic icons are recolored to: the module's
// resolved foreground, falling back to white when it is not a plain RGB
// color (e.g. terminal-default).
func iconColor(ctx *module.Ctx) color.Color {
	if ctx != nil {
		if p := ctx.ActiveBlock().Style().Foreground.Params(); len(p) == 3 {
			return color.RGBA{R: p[0], G: p[1], B: p[2], A: 255}
		}
	}
	return color.White
}

// iconKey is a cache key fully determined by what the decoded icon depends
// on, so an unchanged icon reuses the same decoded image. Empty when the
// item carries no icon.
func iconKey(it sni.Item, fg color.Color) string {
	if it.IconName != "" {
		key := "name:" + it.IconThemeDir + "|" + it.IconName
		if strings.HasSuffix(it.IconName, "-symbolic") {
			key += "|" + colorKey(fg)
		}
		return key
	}
	if it.IconPixmap != nil {
		return "pix:" + it.BusName + "|" + pixmapHash(it.IconPixmap)
	}
	return ""
}

func colorKey(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

func pixmapHash(img image.Image) string {
	h := fnv.New64a()
	if nrgba, ok := img.(*image.NRGBA); ok {
		h.Write(nrgba.Pix)
	}
	b := img.Bounds()
	fmt.Fprintf(h, "%dx%d", b.Dx(), b.Dy())
	return fmt.Sprintf("%x", h.Sum64())
}

// decodeIcon resolves and decodes an item's icon: a themed IconName first
// (honoring the item's IconThemePath), then its embedded IconPixmap. Nil
// when neither yields a usable image.
func decodeIcon(it sni.Item, fg color.Color) image.Image {
	if it.IconName != "" {
		if path := resolveIconPath(it.IconName, it.IconThemeDir); path != "" {
			if img, err := loadIcon(path, it.IconName, fg); err == nil {
				return img
			}
		}
	}
	return it.IconPixmap
}

// resolveIconPath finds a file for name, checking the item's own theme dir
// before the XDG icon themes. Mirrors the menu resolver.
func resolveIconPath(name, themeDir string) string {
	if name == "" {
		return ""
	}
	if p := lookupInDir(themeDir, name); p != "" {
		return p
	}
	var icon xdgicons.Icon
	if strings.HasSuffix(name, "-symbolic") {
		icon, _ = iconLookup.Lookup(name)
	} else {
		icon, _ = iconLookup.FindBestIcon([]string{name + "-symbolic", name}, 48, 2)
	}
	return icon.Path
}

// lookupInDir checks an SNI IconThemePath for a flat name.{svg,png} file.
func lookupInDir(dir, name string) string {
	if dir == "" {
		return ""
	}
	for _, cand := range []string{name + ".svg", name + ".png", name + "-symbolic.svg"} {
		p := filepath.Join(dir, cand)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// loadIcon decodes an icon file, recoloring symbolic SVGs to c. Mirrors the
// menu icon loader.
func loadIcon(path, name string, c color.Color) (image.Image, error) {
	isSymbolic := strings.HasSuffix(name, "-symbolic")
	ext := strings.ToLower(filepath.Ext(path))

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	switch ext {
	case ".svg":
		// Rasterize larger than the drawn size so the renderer's
		// high-quality downscale has detail to work with.
		if isSymbolic {
			return gorsvg.DecodeWithColor(f, 128, 128, c)
		}
		return gorsvg.Decode(f, 128, 128)
	case ".png":
		return png.Decode(f)
	default:
		return nil, fmt.Errorf("unsupported image format: %s", ext)
	}
}
