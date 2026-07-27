package module

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFormatRenderBasics(t *testing.T) {
	f := MustFormat("{icon} {vol}%")
	got, err := f.Render(P{"icon": "V", "vol": 42})
	if err != nil {
		t.Fatal(err)
	}
	if got != "V 42%" {
		t.Errorf("got %q", got)
	}
}

func TestFormatEscapes(t *testing.T) {
	f := MustFormat("{{{vol}}}")
	got, err := f.Render(P{"vol": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got != "{1}" {
		t.Errorf("got %q, want {1}", got)
	}
}

func TestFormatSpecs(t *testing.T) {
	cases := []struct {
		format string
		data   P
		want   string
	}{
		{"{vol:3}", P{"vol": 5}, "  5"},
		{"{load:.2f}", P{"load": 1.5}, "1.50"},
		{"{name:-4}|", P{"name": "ab"}, "ab  |"},
		{"{n:x}", P{"n": 255}, "ff"},
	}
	for _, c := range cases {
		got, err := MustFormat(c.format).Render(c.data)
		if err != nil {
			t.Errorf("%s: %v", c.format, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.format, got, c.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	ts := time.Date(2026, 7, 19, 14, 32, 5, 0, time.UTC)
	got, err := MustFormat("{time:%Y-%m-%d %H:%M}").Render(P{"time": ts})
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-07-19 14:32" {
		t.Errorf("got %q", got)
	}
}

func TestFormatMissingPlaceholder(t *testing.T) {
	_, err := MustFormat("{nope}").Render(P{})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("want error naming the placeholder, got %v", err)
	}
}

func TestFormatCompileErrors(t *testing.T) {
	for _, bad := range []string{
		"{unclosed", "dangling}", "{}",
		"{title~0}", "{title~-1}", "{title~x}", "{~}",
	} {
		if _, err := CompileFormat(bad); err == nil {
			t.Errorf("%q should not compile", bad)
		}
	}
}

func TestFormatShrinkParts(t *testing.T) {
	data := P{"icon": ">", "title": "Song", "artists": "Band"}
	cases := []struct {
		format string
		want   []Part
	}{
		{
			// Elastic pieces split out; the rigid text around them
			// coalesces, so the layout sees as few pieces as possible.
			format: "{icon} {title~} • {artists~}",
			want: []Part{
				{Text: "> "},
				{Text: "Song", Shrink: 1},
				{Text: " • "},
				{Text: "Band", Shrink: 1},
			},
		},
		{
			format: "{title~2} {artists~3}",
			want: []Part{
				{Text: "Song", Shrink: 2},
				{Text: " "},
				{Text: "Band", Shrink: 3},
			},
		},
		{
			// No marker means the whole thing is one rigid piece, as before.
			format: "{icon} {title}",
			want:   []Part{{Text: "> Song"}},
		},
		{
			// A spec still applies to an elastic placeholder.
			format: "{title~2:.2s}",
			want:   []Part{{Text: "So", Shrink: 2}},
		},
	}
	for _, c := range cases {
		got, err := MustFormat(c.format).RenderParts(0, data)
		if err != nil {
			t.Errorf("%s: %v", c.format, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", c.format, got, c.want)
		}
	}
}

// Render must stay the concatenation of the parts, so nothing that only
// wants the flat string has to care about shrinking.
func TestFormatShrinkRenderUnchanged(t *testing.T) {
	data := P{"title": "Song", "artists": "Band"}
	plain, err := MustFormat("{title} • {artists}").Render(data)
	if err != nil {
		t.Fatal(err)
	}
	elastic, err := MustFormat("{title~2} • {artists~}").Render(data)
	if err != nil {
		t.Fatal(err)
	}
	if plain != elastic {
		t.Errorf("shrink markers changed the rendered text: %q vs %q", plain, elastic)
	}
}

func TestFormatShrinkPlaceholderNames(t *testing.T) {
	got := MustFormat("{title~2:.60s} {artists~}").Placeholders()
	want := []string{"title", "artists"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTemplateShrink(t *testing.T) {
	data := P{"title": "Song", "artists": "Band", "vol": 42}
	cases := []struct {
		name string
		tmpl string
		want []Part
	}{
		{
			name: "default weight",
			tmpl: `{{shrink .title}} • {{.artists}}`,
			want: []Part{{Text: "Song", Shrink: 1}, {Text: " • Band"}},
		},
		{
			name: "explicit weight",
			tmpl: `{{shrink 2 .title}}/{{shrink 3 .artists}}`,
			want: []Part{
				{Text: "Song", Shrink: 2},
				{Text: "/"},
				{Text: "Band", Shrink: 3},
			},
		},
		{
			name: "no shrink call is one rigid part",
			tmpl: `{{.title}} {{.vol}}%`,
			want: []Part{{Text: "Song 42%"}},
		},
		{
			// Appending after shrink leaves the delimiters intact, so the
			// value stays elastic and the suffix is rigid.
			name: "appending after shrink still works",
			tmpl: `{{shrink .title}}!`,
			want: []Part{{Text: "Song", Shrink: 1}, {Text: "!"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := MustTemplate(c.tmpl).RenderParts(0, data)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("got %v want %v", got, c.want)
			}
			for _, p := range got {
				if strings.Contains(p.Text, "\x00") {
					t.Errorf("part %q leaked a delimiter", p.Text)
				}
			}
		})
	}
}

// A pipeline that rewrites shrink's output (quoting, case folding) destroys
// the delimiters. The text must still come through as ordinary rigid text
// rather than leaking control characters into the bar.
func TestTemplateShrinkMangledDegradesToRigid(t *testing.T) {
	got, err := MustTemplate(`{{shrink .title | printf "%q"}}`).RenderParts(0, P{"title": "Song"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Shrink != 0 {
		t.Errorf("want one rigid part, got %v", got)
	}
	for _, p := range got {
		if strings.Contains(p.Text, "\x00") {
			t.Errorf("part %q leaked a delimiter", p.Text)
		}
	}
}

func TestTemplateShrinkBadArgs(t *testing.T) {
	for _, bad := range []string{
		`{{shrink 0 .title}}`,
		`{{shrink -1 .title}}`,
		`{{shrink}}`,
		`{{shrink 1 2 .title}}`,
	} {
		if _, err := MustTemplate(bad).Render(P{"title": "Song"}); err == nil {
			t.Errorf("%q should fail to render", bad)
		}
	}
}

func TestFormatPlaceholders(t *testing.T) {
	f := MustFormat("{a} {b} {a:3}")
	got := f.Placeholders()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v", got)
	}
}

func TestTimeGranularity(t *testing.T) {
	cases := []struct {
		format string
		want   time.Duration
	}{
		{"{time:%H:%M:%S}", time.Second},
		{"{time:%H:%M}", time.Minute},
		{"{time:%Y-%m-%d}", 24 * time.Hour},
		{"{time:%H}", time.Hour},
		{"{vol:3} {icon}", 0},
		{"{t:%H:%M} {u:%S}", time.Second},
	}
	for _, c := range cases {
		if got := MustFormat(c.format).TimeGranularity(); got != c.want {
			t.Errorf("%s: got %v want %v", c.format, got, c.want)
		}
	}
}

func TestTemplateRender(t *testing.T) {
	tpl := MustTemplate("{{.icon}} {{round .vol}}%")
	got, err := tpl.Render(P{"icon": "V", "vol": 41.6})
	if err != nil {
		t.Fatal(err)
	}
	if got != "V 42%" {
		t.Errorf("got %q", got)
	}
}
