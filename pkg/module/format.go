// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/itchyny/timefmt-go"
	"gopkg.in/yaml.v3"
)

// P carries the placeholder values a module exposes to its format string,
// keyed by placeholder name.
type P map[string]any

// Part is one piece of rendered output. Shrink marks the piece elastic
// with that flex weight: when the bar runs out of room, elastic pieces give
// way — in proportion to their weights — before anything else is touched.
// Shrink 0 is rigid.
type Part struct {
	Text   string
	Shrink int
}

// Formatter renders placeholder data to the text shown in the bar. Format
// (placeholder syntax) and Template (Go text/template) both implement it.
//
// A formatter can offer several detail levels, widest first: when the bar
// runs short of room the layout steps down to a more compact level rather
// than truncating. Level 0 is what a module shows given the space.
type Formatter interface {
	Render(data P) (string, error)
	// Levels is how many detail levels this formatter offers; at least 1.
	Levels() int
	// RenderParts renders one level to pieces, so the layout can shrink
	// the elastic ones independently. The level is clamped to range.
	RenderParts(level int, data P) ([]Part, error)
}

// Format is a compiled placeholder format string.
//
// Syntax:
//
//	{name}          value formatted with fmt.Sprint
//	{name:spec}     value formatted with spec
//	{name~}         value, elastic with weight 1
//	{name~2}        value, elastic with weight 2
//	{{  }}          literal braces
//
// For time.Time values the spec is a strftime layout ({time:%H:%M}).
// For everything else the spec is a printf spec without the leading '%':
// a trailing letter is used as the verb ({load:.2f}), otherwise the verb
// is inferred ({vol:3} -> %3v). The two combine: {title~2:.60s}.
//
// A Format may hold several levels (compiled from a yaml list), widest
// first; the layout steps down them when space runs out.
type Format struct {
	srcs []string
	alts [][]ftok
}

type ftok struct {
	lit    string // literal text; used when name == ""
	name   string
	spec   string
	shrink int // flex weight; 0 for rigid pieces and literals
}

// CompileFormat parses a placeholder format string.
func CompileFormat(s string) (*Format, error) {
	toks, err := compileTokens(s)
	if err != nil {
		return nil, err
	}
	return &Format{srcs: []string{s}, alts: [][]ftok{toks}}, nil
}

// CompileFormatList parses a ladder of formats, widest first. The layout
// steps down the list when the bar runs short of room, so later entries
// should show strictly less than earlier ones.
func CompileFormatList(ss []string) (*Format, error) {
	if len(ss) == 0 {
		return nil, fmt.Errorf("format: a list of formats must not be empty")
	}
	f := &Format{srcs: slices.Clone(ss)}
	for _, s := range ss {
		toks, err := compileTokens(s)
		if err != nil {
			return nil, err
		}
		f.alts = append(f.alts, toks)
	}
	return f, nil
}

func compileTokens(s string) ([]ftok, error) {
	var toks []ftok
	var lit strings.Builder
	i := 0
	flushLit := func() {
		if lit.Len() > 0 {
			toks = append(toks, ftok{lit: lit.String()})
			lit.Reset()
		}
	}
	for i < len(s) {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			lit.WriteByte('{')
			i += 2
		case strings.HasPrefix(s[i:], "}}"):
			lit.WriteByte('}')
			i += 2
		case s[i] == '}':
			return nil, fmt.Errorf("format %q: unmatched '}' at column %d (use '}}' for a literal)", s, i+1)
		case s[i] == '{':
			body, rest, ok := strings.Cut(s[i+1:], "}")
			if !ok {
				return nil, fmt.Errorf("format %q: unclosed '{' at column %d (use '{{' for a literal)", s, i+1)
			}
			name, spec, _ := strings.Cut(body, ":")
			// The spec is cut off first, so {title~2:.60s} splits cleanly.
			shrink := 0
			if base, weight, marked := strings.Cut(name, "~"); marked {
				name, shrink = base, 1
				if weight != "" {
					w, err := strconv.Atoi(weight)
					if err != nil || w <= 0 {
						return nil, fmt.Errorf(
							"format %q: shrink weight %q at column %d must be a positive integer",
							s, weight, i+1)
					}
					shrink = w
				}
			}
			if name == "" {
				return nil, fmt.Errorf("format %q: empty placeholder at column %d", s, i+1)
			}
			flushLit()
			toks = append(toks, ftok{name: name, spec: spec, shrink: shrink})
			i = len(s) - len(rest)
		default:
			lit.WriteByte(s[i])
			i++
		}
	}
	flushLit()
	return toks, nil
}

// MustFormat compiles a placeholder format and panics on failure. Intended
// for Def defaults built from constants.
func MustFormat(s string) *Format {
	f, err := CompileFormat(s)
	if err != nil {
		panic(err)
	}
	return f
}

// UnmarshalYAML accepts a single format, or a list of them widest first:
//
//	format: "{icon} {bat}%"
//	format: ["{icon} {bat}% ({time})", "{icon} {bat}%", "{icon}"]
func (f *Format) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		var ss []string
		if err := n.Decode(&ss); err != nil {
			return err
		}
		c, err := CompileFormatList(ss)
		if err != nil {
			return err
		}
		*f = *c
		return nil
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	c, err := CompileFormat(s)
	if err != nil {
		return err
	}
	*f = *c
	return nil
}

// String returns the source text the format was compiled from; a ladder
// renders as its levels in order.
func (f *Format) String() string {
	if len(f.srcs) == 1 {
		return f.srcs[0]
	}
	return "[" + strings.Join(f.srcs, ", ") + "]"
}

func (f *Format) MarshalYAML() (any, error) {
	if len(f.srcs) == 1 {
		return f.srcs[0], nil
	}
	return f.srcs, nil
}

// Levels is how many detail levels the format offers.
func (f *Format) Levels() int { return len(f.alts) }

// Placeholders returns the distinct placeholder names referenced, across
// every level.
func (f *Format) Placeholders() []string {
	seen := make(map[string]bool)
	var out []string
	for _, toks := range f.alts {
		for _, t := range toks {
			if t.name != "" && !seen[t.name] {
				seen[t.name] = true
				out = append(out, t.name)
			}
		}
	}
	return out
}

func (f *Format) Render(data P) (string, error) {
	parts, err := f.RenderParts(0, data)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

// RenderParts renders each elastic placeholder as its own part; everything
// between them coalesces into rigid parts.
func (f *Format) RenderParts(level int, data P) ([]Part, error) {
	level = min(max(level, 0), len(f.alts)-1)
	src := f.srcs[level]

	var out []Part
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, Part{Text: lit.String()})
			lit.Reset()
		}
	}
	for _, t := range f.alts[level] {
		if t.name == "" {
			lit.WriteString(t.lit)
			continue
		}
		v, ok := data[t.name]
		if !ok {
			return nil, fmt.Errorf("format %q: no value for placeholder %q", src, t.name)
		}
		s, err := formatValue(v, t.spec)
		if err != nil {
			return nil, fmt.Errorf("format %q: {%s:%s}: %w", src, t.name, t.spec, err)
		}
		if t.shrink == 0 {
			lit.WriteString(s)
			continue
		}
		flush()
		out = append(out, Part{Text: s, Shrink: t.shrink})
	}
	flush()
	return out, nil
}

func formatValue(v any, spec string) (string, error) {
	if t, ok := v.(time.Time); ok {
		if spec == "" {
			spec = "%H:%M"
		}
		return timefmt.Format(t, spec), nil
	}
	if spec == "" {
		return fmt.Sprint(v), nil
	}
	last := spec[len(spec)-1]
	if last >= 'a' && last <= 'z' || last >= 'A' && last <= 'Z' {
		return fmt.Sprintf("%"+spec, v), nil
	}
	return fmt.Sprintf("%"+spec+"v", v), nil
}

// TimeGranularity inspects every strftime spec in the format and returns
// the smallest time unit it displays, or 0 if no time directives appear.
// Modules use it to derive an automatic tick interval from the format.
//
// It folds across every level, so the tick stays fast enough for whichever
// level the layout ends up showing.
func (f *Format) TimeGranularity() time.Duration {
	g := time.Duration(0)
	for _, toks := range f.alts {
		for _, t := range toks {
			if t.name == "" || t.spec == "" || !strings.Contains(t.spec, "%") {
				continue
			}
			if d := StrftimeGranularity(t.spec); d > 0 && (g == 0 || d < g) {
				g = d
			}
		}
	}
	return g
}

// StrftimeGranularity returns the smallest time unit a strftime layout
// displays, or 0 if it contains no known directives.
func StrftimeGranularity(layout string) time.Duration {
	containsAny := func(needles ...string) bool {
		for _, n := range needles {
			if strings.Contains(layout, n) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("%L", "%N", "%f"):
		return time.Millisecond
	case containsAny("%S", "%s", "%T", "%X", "%c"):
		return time.Second
	case containsAny("%M", "%R"):
		return time.Minute
	case containsAny("%H", "%I", "%k", "%l", "%p", "%P"):
		return time.Hour
	case containsAny("%d", "%e", "%j", "%a", "%A", "%u", "%w",
		"%m", "%b", "%B", "%y", "%Y", "%D", "%F", "%x"):
		return 24 * time.Hour
	default:
		return 0
	}
}

// Template is an opt-in Go text/template Formatter for power users; the
// yaml key is "template". The placeholder data P is the template's dot.
//
// Like Format it may hold several levels, compiled from a yaml list.
type Template struct {
	srcs []string
	ts   []*template.Template
}

// shrinkMark delimits a `shrink` helper's output inside a rendered
// template. A template renders to one flat string, so marking elastic
// pieces in-band is the only way to recover their boundaries afterwards.
// NUL never survives into real bar text, and the helper strips it from
// values, so it cannot collide.
const shrinkMark = "\x00"

var templateFuncs = template.FuncMap{
	// shrink marks its value elastic: shrink .title, or shrink 2 .title
	// to weight it. Appending after it is fine, but a pipeline stage that
	// rewrites its output (quoting, case folding) destroys the delimiters
	// and the value degrades to ordinary rigid text.
	"shrink": func(args ...any) (string, error) {
		weight, v := 1, any(nil)
		switch len(args) {
		case 1:
			v = args[0]
		case 2:
			w, ok := toInt(args[0])
			if !ok || w <= 0 {
				return "", fmt.Errorf("shrink: weight must be a positive number, got %v", args[0])
			}
			weight, v = w, args[1]
		default:
			return "", fmt.Errorf("shrink: want (value) or (weight, value), got %d arguments", len(args))
		}
		text := strings.ReplaceAll(fmt.Sprint(v), shrinkMark, "")
		return shrinkMark + strconv.Itoa(weight) + shrinkMark + text + shrinkMark, nil
	},
	"round": func(v any) (int64, error) {
		switch n := v.(type) {
		case float64:
			return int64(math.Round(n)), nil
		case float32:
			return int64(math.Round(float64(n))), nil
		case int:
			return int64(n), nil
		case int64:
			return n, nil
		default:
			return 0, fmt.Errorf("round: unsupported type %T", v)
		}
	},
	"strftime": func(layout string, t time.Time) string {
		return timefmt.Format(t, layout)
	},
}

// CompileTemplate parses a Go text/template format.
func CompileTemplate(s string) (*Template, error) {
	t, err := parseTemplate(s)
	if err != nil {
		return nil, err
	}
	return &Template{srcs: []string{s}, ts: []*template.Template{t}}, nil
}

// CompileTemplateList parses a ladder of templates, widest first — the
// template counterpart of CompileFormatList.
func CompileTemplateList(ss []string) (*Template, error) {
	if len(ss) == 0 {
		return nil, fmt.Errorf("template: a list of templates must not be empty")
	}
	out := &Template{srcs: slices.Clone(ss)}
	for _, s := range ss {
		t, err := parseTemplate(s)
		if err != nil {
			return nil, err
		}
		out.ts = append(out.ts, t)
	}
	return out, nil
}

func parseTemplate(s string) (*template.Template, error) {
	return template.New("format").Funcs(templateFuncs).Parse(s)
}

// MustTemplate compiles a template and panics on failure.
func MustTemplate(s string) *Template {
	t, err := CompileTemplate(s)
	if err != nil {
		panic(err)
	}
	return t
}

// UnmarshalYAML accepts a single template or a list of them, widest first.
func (t *Template) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		var ss []string
		if err := n.Decode(&ss); err != nil {
			return err
		}
		c, err := CompileTemplateList(ss)
		if err != nil {
			return err
		}
		*t = *c
		return nil
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	c, err := CompileTemplate(s)
	if err != nil {
		return err
	}
	*t = *c
	return nil
}

// String returns the source text the template was compiled from; a ladder
// renders as its levels in order.
func (t *Template) String() string {
	if len(t.srcs) == 1 {
		return t.srcs[0]
	}
	return "[" + strings.Join(t.srcs, ", ") + "]"
}

func (t *Template) MarshalYAML() (any, error) {
	if len(t.srcs) == 1 {
		return t.srcs[0], nil
	}
	return t.srcs, nil
}

// Levels is how many detail levels the template offers.
func (t *Template) Levels() int { return len(t.ts) }

func (t *Template) Render(data P) (string, error) {
	parts, err := t.RenderParts(0, data)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

// RenderParts executes the template and splits the `shrink` helper's
// markers back out. A template without shrink calls is one rigid part.
func (t *Template) RenderParts(level int, data P) ([]Part, error) {
	level = min(max(level, 0), len(t.ts)-1)
	var b strings.Builder
	if err := t.ts[level].Execute(&b, map[string]any(data)); err != nil {
		return nil, err
	}
	return splitShrink(b.String()), nil
}

// splitShrink recovers the parts the shrink helper marked. A marker run
// that doesn't parse — because something downstream in the pipeline
// rewrote it — drops the delimiters and keeps the text as rigid.
func splitShrink(s string) []Part {
	if !strings.Contains(s, shrinkMark) {
		if s == "" {
			return nil
		}
		return []Part{{Text: s}}
	}

	var out []Part
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, Part{Text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] != shrinkMark[0] {
			lit.WriteByte(s[i])
			i++
			continue
		}
		// <NUL> weight <NUL> text <NUL>
		wEnd := strings.IndexByte(s[i+1:], shrinkMark[0])
		if wEnd < 0 {
			i++ // stray delimiter: drop it
			continue
		}
		tStart := i + 1 + wEnd + 1
		tEnd := strings.IndexByte(s[tStart:], shrinkMark[0])
		w, err := strconv.Atoi(s[i+1 : i+1+wEnd])
		if tEnd < 0 || err != nil || w <= 0 {
			i++
			continue
		}
		flush()
		out = append(out, Part{Text: s[tStart : tStart+tEnd], Shrink: w})
		i = tStart + tEnd + 1
	}
	flush()
	return out
}

// toInt coerces a template argument to an int, since template literals
// arrive as int and pipeline values may be floats.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == math.Trunc(n)
	default:
		return 0, false
	}
}
