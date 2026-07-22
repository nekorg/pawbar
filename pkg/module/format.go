// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package module

import (
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"

	"github.com/itchyny/timefmt-go"
	"gopkg.in/yaml.v3"
)

// P carries the placeholder values a module exposes to its format string,
// keyed by placeholder name.
type P map[string]any

// Formatter renders placeholder data to the text shown in the bar. Format
// (placeholder syntax) and Template (Go text/template) both implement it.
type Formatter interface {
	Render(data P) (string, error)
}

// Format is a compiled placeholder format string.
//
// Syntax:
//
//	{name}          value formatted with fmt.Sprint
//	{name:spec}     value formatted with spec
//	{{  }}          literal braces
//
// For time.Time values the spec is a strftime layout ({time:%H:%M}).
// For everything else the spec is a printf spec without the leading '%':
// a trailing letter is used as the verb ({load:.2f}), otherwise the verb
// is inferred ({vol:3} -> %3v).
type Format struct {
	src  string
	toks []ftok
}

type ftok struct {
	lit  string // literal text; used when name == ""
	name string
	spec string
}

// CompileFormat parses a placeholder format string.
func CompileFormat(s string) (*Format, error) {
	f := &Format{src: s}
	var lit strings.Builder
	i := 0
	flushLit := func() {
		if lit.Len() > 0 {
			f.toks = append(f.toks, ftok{lit: lit.String()})
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
			if name == "" {
				return nil, fmt.Errorf("format %q: empty placeholder at column %d", s, i+1)
			}
			flushLit()
			f.toks = append(f.toks, ftok{name: name, spec: spec})
			i = len(s) - len(rest)
		default:
			lit.WriteByte(s[i])
			i++
		}
	}
	flushLit()
	return f, nil
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

func (f *Format) UnmarshalYAML(n *yaml.Node) error {
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

// String returns the source text the format was compiled from.
func (f *Format) String() string { return f.src }

func (f *Format) MarshalYAML() (any, error) { return f.src, nil }

// Placeholders returns the distinct placeholder names referenced.
func (f *Format) Placeholders() []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range f.toks {
		if t.name != "" && !seen[t.name] {
			seen[t.name] = true
			out = append(out, t.name)
		}
	}
	return out
}

func (f *Format) Render(data P) (string, error) {
	var b strings.Builder
	for _, t := range f.toks {
		if t.name == "" {
			b.WriteString(t.lit)
			continue
		}
		v, ok := data[t.name]
		if !ok {
			return "", fmt.Errorf("format %q: no value for placeholder %q", f.src, t.name)
		}
		s, err := formatValue(v, t.spec)
		if err != nil {
			return "", fmt.Errorf("format %q: {%s:%s}: %w", f.src, t.name, t.spec, err)
		}
		b.WriteString(s)
	}
	return b.String(), nil
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
func (f *Format) TimeGranularity() time.Duration {
	g := time.Duration(0)
	for _, t := range f.toks {
		if t.name == "" || t.spec == "" || !strings.Contains(t.spec, "%") {
			continue
		}
		if d := StrftimeGranularity(t.spec); d > 0 && (g == 0 || d < g) {
			g = d
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
type Template struct {
	src string
	t   *template.Template
}

var templateFuncs = template.FuncMap{
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
	t, err := template.New("format").Funcs(templateFuncs).Parse(s)
	if err != nil {
		return nil, err
	}
	return &Template{src: s, t: t}, nil
}

// MustTemplate compiles a template and panics on failure.
func MustTemplate(s string) *Template {
	t, err := CompileTemplate(s)
	if err != nil {
		panic(err)
	}
	return t
}

func (t *Template) UnmarshalYAML(n *yaml.Node) error {
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

// String returns the source text the template was compiled from.
func (t *Template) String() string { return t.src }

func (t *Template) MarshalYAML() (any, error) { return t.src, nil }

func (t *Template) Render(data P) (string, error) {
	var b strings.Builder
	if err := t.t.Execute(&b, map[string]any(data)); err != nil {
		return "", err
	}
	return b.String(), nil
}
