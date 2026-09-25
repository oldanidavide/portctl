package sshutil

import (
	"reflect"
	"strings"
	"testing"

	"github.com/oldanidavide/portctl/internal/config"
)

func TestArgs(t *testing.T) {
	r := config.Resolved{Port: 8000, RemotePort: 8000, RemoteHost: "127.0.0.1", User: "davide", Host: "production", SSHPort: 22}
	got := Args(r)
	want := []string{"-N", "-L", "127.0.0.1:8000:127.0.0.1:8000", "-o", "ExitOnForwardFailure=yes", "-o", "ControlPath=none", "davide@production"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestBackgroundArgs(t *testing.T) {
	r := config.Resolved{Port: 8000, RemotePort: 8000, RemoteHost: "127.0.0.1", User: "davide", Host: "production", SSHPort: 22}
	got := BackgroundArgs(r)
	if got[0] != "-f" || !reflect.DeepEqual(got[1:], Args(r)) {
		t.Fatalf("got %#v", got)
	}
	if s := String(r); !strings.HasPrefix(s, "ssh -f -N -L") {
		t.Fatalf("String = %q", s)
	}
}

func TestArgsOptions(t *testing.T) {
	r := config.Resolved{Port: 8000, RemotePort: 9000, RemoteHost: "127.0.0.1", User: "d", Host: "h", SSHPort: 2200, IdentityFile: "/tmp/key", Keepalive: config.KeepaliveConfig{Enabled: true, Interval: 30, CountMax: 3}}
	s := String(r)
	for _, x := range []string{"-p 2200", "-i /tmp/key", "ServerAliveInterval=30", "ServerAliveCountMax=3"} {
		if !strings.Contains(s, x) {
			t.Errorf("missing %q in %q", x, s)
		}
	}
}

func TestJoinQuotes(t *testing.T) {
	if got := Join([]string{"-i", "/my keys/id", "it's", ""}); got != `ssh -i '/my keys/id' 'it'\''s' ''` {
		t.Fatalf("got %s", got)
	}
}
