package ui

import (
	"os"
	"testing"
)

func TestStyleOff(t *testing.T) {
	if got := (Style{}).Red("x"); got != "x" {
		t.Fatalf("disabled style must not add escapes, got %q", got)
	}
}

func TestStyleOn(t *testing.T) {
	s := Style{on: true}
	if got := s.Green("ok"); got != "\x1b[32mok\x1b[0m" {
		t.Fatalf("got %q", got)
	}
	if got := s.Red(""); got != "" {
		t.Fatalf("empty text must stay empty, got %q", got)
	}
}

func TestNoColorEnv(t *testing.T) {
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("NO_COLOR", "1")
	if enabled(os.Stdout) {
		t.Fatal("NO_COLOR must disable colors")
	}
}

func TestForceColorEnv(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	if !enabled(os.Stdout) {
		t.Fatal("FORCE_COLOR must enable colors")
	}
}
