// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package textrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/nekorg/pawbar/internal/logging"
)

// metrics are everything we need to draw a glyph where the terminal would
// have drawn one: the em the terminal rasterises at, and the baseline it
// puts every face on, however far that face's own metrics sit from it.
type metrics struct {
	em       float64
	baseline int
	family   string
	// mediumPath is the face the terminal draws ordinary text with. Latin
	// and punctuation inside a complex run come from it so the phrase does
	// not change typeface halfway.
	mediumPath string
	// symbolMap is the user's kitty symbol_map, which is how they name a
	// face for a script. Ours to honour, since we are picking the face.
	symbolMap []symbolRange
}

type symbolRange struct {
	lo, hi rune
	family string
}

// probeSrc asks kitty for its own answer rather than reimplementing it. It
// runs kitty's config loader, so include directives, BEGIN_KITTY_FONTS blocks
// and font_family aliases all resolve exactly as they do for a real window.
//
// dpi is not something we can look up: it depends on the output scale the
// panel landed on. Cell height is linear in it, so the loop converges on the
// height vaxis actually measured in one or two steps.
//
// Flat, loop-only code on purpose: `kitty +runpy` execs without a globals
// dict, so functions and comprehensions here lose module level names.
const probeSrc = `
import json, os
from kitty.config import load_config
from kitty.constants import config_dir
import kitty.fonts.render as R

conf = %s
if conf == 'INHERIT':
    conf = os.path.join(config_dir, 'kitty.conf')
    if not os.path.exists(conf):
        conf = None
opts = load_config(conf) if conf else load_config()
fam = %s
if not fam:
    fam = opts.font_family.family or 'monospace'
sz = %f
targetH = %d

dpi = 96.0
cw = 0
ch = 0
bl = 0
med = ''
n = 0
while n < 6:
    st = R.setup_for_testing(fam, sz, dpi)
    with st as (sprites, w, h):
        cw = w
        ch = h
        bl = st.baseline
        med = R.current_fonts()['medium'].identify_for_debug()
    if not targetH or ch == targetH:
        break
    dpi = dpi * float(targetH) / float(ch)
    n += 1

sm = []
for k in opts.symbol_map:
    sm.append([k[0], k[1], opts.symbol_map[k]])

print('PAWBAR' + json.dumps({'family': fam, 'size': sz, 'dpi': dpi,
      'cell': [cw, ch], 'baseline': bl, 'medium': med, 'symbol_map': sm}))
`

type probeResult struct {
	Family    string          `json:"family"`
	Size      float64         `json:"size"`
	DPI       float64         `json:"dpi"`
	Cell      [2]int          `json:"cell"`
	Baseline  int             `json:"baseline"`
	Medium    string          `json:"medium"`
	SymbolMap [][]interface{} `json:"symbol_map"`
}

// probe asks kitty for the metrics of a cell targetH pixels tall. A failure
// is not fatal: [deriveMetrics] reproduces kitty's arithmetic from the face
// we resolve ourselves, it just cannot see the user's symbol_map.
func probe(o Options, targetH int) (*metrics, error) {
	// An empty config file is kitty's "inherit"; only "NONE" really means
	// the built-in defaults.
	conf := "'INHERIT'"
	switch o.KittyConfig {
	case "":
	case "NONE":
		conf = "None"
	default:
		conf = pyString(o.KittyConfig)
	}
	fam := "None"
	if o.Family != "" {
		fam = pyString(o.Family)
	}
	src := fmt.Sprintf(probeSrc, conf, fam, o.FontSize, targetH)

	cmd := o.KittyCmd
	if cmd == "" {
		cmd = "kitty"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmd, "+runpy", src).Output()
	if err != nil {
		return nil, fmt.Errorf("running the kitty font probe: %w", err)
	}

	// kitty prints config warnings on the way; ours is the tagged line.
	line := ""
	for _, l := range strings.Split(string(out), "\n") {
		if s, ok := strings.CutPrefix(l, "PAWBAR"); ok {
			line = s
		}
	}
	if line == "" {
		return nil, fmt.Errorf("the kitty font probe printed nothing usable")
	}

	var r probeResult
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		return nil, fmt.Errorf("parsing the kitty font probe: %w", err)
	}
	if r.Cell[1] != targetH {
		logging.Log.Debug().Msgf("textrun: kitty says a cell is %dx%d, vaxis measured a height of %d",
			r.Cell[0], r.Cell[1], targetH)
	}

	m := &metrics{
		em:         r.Size * r.DPI / 72.0,
		baseline:   r.Baseline,
		family:     r.Family,
		mediumPath: facePath(r.Medium),
	}
	for _, e := range r.SymbolMap {
		if len(e) != 3 {
			continue
		}
		lo, lok := e[0].(float64)
		hi, hok := e[1].(float64)
		fam, fok := e[2].(string)
		if lok && hok && fok {
			m.symbolMap = append(m.symbolMap, symbolRange{rune(lo), rune(hi), fam})
		}
	}
	return m, nil
}

// facePath pulls the file out of kitty's face description, which reads
// "Family: /path/to/file.ttf:0" possibly followed by feature lines.
func facePath(desc string) string {
	line, _, _ := strings.Cut(desc, "\n")
	_, rest, ok := strings.Cut(line, ": ")
	if !ok {
		return ""
	}
	if i := strings.LastIndexByte(rest, ':'); i > 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

func pyString(s string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(s) + "'"
}

// familyFor returns the face the user named for r in symbol_map, if any.
func (m *metrics) familyFor(r rune) string {
	for _, e := range m.symbolMap {
		if r >= e.lo && r <= e.hi {
			return e.family
		}
	}
	return ""
}
