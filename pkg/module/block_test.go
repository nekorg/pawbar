package module

import (
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

func TestBlockOver(t *testing.T) {
	base := Block{
		Fg:     MustColor("#ff0000"),
		Bg:     MustColor("#000000"),
		Bold:   Ptr(true),
		Format: MustFormat("{a}"),
	}
	over := Block{
		Fg:     MustColor("#00ff00"),
		Italic: Ptr(true),
	}

	got := over.Over(base)

	if got.Fg.Go() != MustColor("#00ff00").Go() {
		t.Errorf("Fg: overriding layer should win")
	}
	if got.Bg == nil || got.Bg.Go() != MustColor("#000000").Go() {
		t.Errorf("Bg: unset field should inherit from base")
	}
	if got.Bold == nil || !*got.Bold {
		t.Errorf("Bold: unset field should inherit from base")
	}
	if got.Italic == nil || !*got.Italic {
		t.Errorf("Italic: overriding layer should win")
	}
	if got.Format == nil || got.Format.String() != "{a}" {
		t.Errorf("Format: unset field should inherit from base")
	}
}

func TestBlockOverFalseBeatsTrue(t *testing.T) {
	base := Block{Bold: Ptr(true)}
	over := Block{Bold: Ptr(false)}
	got := over.Over(base)
	if got.Bold == nil || *got.Bold {
		t.Errorf("explicit false must override inherited true (pointer semantics)")
	}
}

func TestBlockOverFormatMasksTemplate(t *testing.T) {
	base := Block{Template: MustTemplate("{{.a}}")}
	over := Block{Format: MustFormat("{a}")}
	got := over.Over(base)
	if got.Template != nil {
		t.Errorf("setting Format should clear inherited Template")
	}
	if got.Format == nil {
		t.Errorf("Format should be set")
	}

	back := Block{Template: MustTemplate("{{.b}}")}.Over(got)
	if back.Format != nil {
		t.Errorf("setting Template should clear inherited Format")
	}
}

func TestBlockOverChainPriority(t *testing.T) {
	themeDefault := Block{Fg: MustColor("#111111"), Bg: MustColor("#222222")}
	entry := Block{Fg: MustColor("#333333")}
	state := Block{Bg: MustColor("#444444")}

	got := state.Over(entry.Over(themeDefault))

	if got.Fg.Go() != MustColor("#333333").Go() {
		t.Errorf("entry Fg should survive state layer that doesn't set Fg")
	}
	if got.Bg.Go() != MustColor("#444444").Go() {
		t.Errorf("state Bg should win over theme Bg")
	}
}

func TestBlockStyle(t *testing.T) {
	b := Block{
		Fg:        MustColor("#abcdef"),
		Bold:      Ptr(true),
		Underline: Ptr(true),
		Dim:       Ptr(false),
	}
	s := b.Style()
	if s.Attribute&vaxis.AttrBold == 0 {
		t.Errorf("bold should be set")
	}
	if s.Attribute&vaxis.AttrDim != 0 {
		t.Errorf("explicit false must not set dim")
	}
	if s.UnderlineStyle != vaxis.UnderlineSingle {
		t.Errorf("underline should be single")
	}
	if s.Foreground != MustColor("#abcdef").Go() {
		t.Errorf("foreground mismatch")
	}
}

func TestBlockFormatter(t *testing.T) {
	if (Block{}).Formatter() != nil {
		t.Errorf("empty block has no formatter")
	}
	b := Block{Format: MustFormat("{a}"), Template: MustTemplate("{{.a}}")}
	if _, ok := b.Formatter().(*Template); !ok {
		t.Errorf("template should win when both set")
	}
}
