package module

import (
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
	for _, bad := range []string{"{unclosed", "dangling}", "{}"} {
		if _, err := CompileFormat(bad); err == nil {
			t.Errorf("%q should not compile", bad)
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
