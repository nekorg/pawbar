package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchConfigSignalsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pawbar.yaml")
	if err := os.WriteFile(path, []byte("right: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	defer close(done)
	ch, err := WatchConfig(done, path, t.Logf)
	if err != nil {
		t.Fatal(err)
	}

	// In-place write.
	if err := os.WriteFile(path, []byte("left: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("no signal after write")
	}

	// Editor-style rename-replace.
	tmp := filepath.Join(dir, "pawbar.yaml.new")
	if err := os.WriteFile(tmp, []byte("middle: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("no signal after rename-replace")
	}

	// Unrelated files stay quiet.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Fatal("signal for unrelated file")
	case <-time.After(400 * time.Millisecond):
	}
}
