package monitor

import "testing"

func TestWithOutputReplacesExisting(t *testing.T) {
	env := []string{"HOME=/home/x", "PAWBAR_OUTPUT=stale", "TERM=xterm"}
	got := WithOutput(env, "eDP-1")
	want := []string{"HOME=/home/x", "TERM=xterm", "PAWBAR_OUTPUT=eDP-1"}
	if len(got) != len(want) {
		t.Fatalf("WithOutput = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("WithOutput = %v, want %v", got, want)
		}
	}
	// The caller's slice must not be clobbered: katnip reuses it.
	if env[1] != "PAWBAR_OUTPUT=stale" {
		t.Errorf("WithOutput mutated its input: %v", env)
	}
}
