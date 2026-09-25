package cli

import (
	"os"
	"testing"

	"github.com/oldanidavide/portctl/internal/config"
	"github.com/oldanidavide/portctl/internal/ports"
	"github.com/oldanidavide/portctl/internal/state"
)

func quietStderr(t *testing.T) {
	t.Helper()
	old := os.Stderr
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = old; f.Close() })
}

func TestParsePortArgsFlagsAnywhere(t *testing.T) {
	quietStderr(t)
	for _, args := range [][]string{
		{"8000", "--dry-run", "-u", "x"},
		{"--dry-run", "8000", "-u", "x"},
		{"-u", "x", "--dry-run", "8000"},
	} {
		fs := newFlags("test", "<port>")
		dry := fs.Bool("dry-run", false, "")
		user := fs.String("u", "", "")
		p, rc, ok := parsePortArgs(fs, args)
		if !ok || rc != 0 || p != 8000 || !*dry || *user != "x" {
			t.Errorf("%q: port=%d rc=%d ok=%v dry=%v user=%q", args, p, rc, ok, *dry, *user)
		}
	}
}

func TestParsePortArgsErrors(t *testing.T) {
	quietStderr(t)
	for _, c := range []struct {
		args []string
		rc   int
	}{
		{nil, 2},
		{[]string{"abc"}, 2},
		{[]string{"0"}, 2},
		{[]string{"65536"}, 2},
		{[]string{"80", "81"}, 2},
		{[]string{"80", "--nope"}, 2},
		{[]string{"--help"}, 0},
	} {
		_, rc, ok := parsePortArgs(newFlags("test", "<port>"), c.args)
		if ok || rc != c.rc {
			t.Errorf("%q: ok=%v rc=%d, want rc=%d", c.args, ok, rc, c.rc)
		}
	}
}

func TestMergedAppliesOverrides(t *testing.T) {
	c := config.Default()
	c.SSH.User, c.SSH.Host = "u", "h"
	c.Tunnels = []config.TunnelConfig{{Port: 80, RemotePort: 8080, Name: "web"}}
	r := merged(c, 80, state.Overrides{Host: "other", Identity: "/k"})
	if r.User != "u" || r.Host != "other" || r.IdentityFile != "/k" || r.RemotePort != 8080 || r.Name != "web" {
		t.Fatalf("got %+v", r)
	}
	if r := merged(c, 9999, state.Overrides{}); r.RemotePort != 9999 || r.Host != "h" {
		t.Fatalf("unconfigured port: got %+v", r)
	}
}

func TestResolveWithoutConfig(t *testing.T) {
	quietStderr(t)
	a := &App{ConfigPath: t.TempDir() + "/missing.yaml", StateDir: t.TempDir()}
	if _, err := a.resolve(80, state.Overrides{}); err == nil {
		t.Fatal("expected an error without config and without --host")
	}
	r, err := a.resolve(80, state.Overrides{User: "u", Host: "h"})
	if err != nil || r.SSHPort != 22 || r.RemoteHost != "127.0.0.1" {
		t.Fatalf("got %+v, %v", r, err)
	}
	if _, err := a.resolve(80, state.Overrides{User: "u", Host: "-oProxyCommand=x"}); err == nil {
		t.Fatal("expected rejection of a host starting with '-'")
	}
}

func TestExposed(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1":        false,
		"127.0.0.1, [::1]": false,
		"*":                true,
		"0.0.0.0":          true,
		"127.0.0.1, *":     true,
		"192.168.1.10":     true,
	} {
		if got := exposed(addr); got != want {
			t.Errorf("exposed(%q) = %v", addr, got)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := truncate("àbcdefghij", 5); got != "àbcd…" {
		t.Errorf("got %q", got)
	}
}

func TestKillable(t *testing.T) {
	for _, l := range []ports.Listener{
		{PID: 0, Port: 80},
		{PID: 1, Port: 80},
		{PID: os.Getpid(), Port: 80, UID: -1},
		{PID: os.Getppid(), Port: 80, UID: -1},
	} {
		if killable(l) == nil {
			t.Errorf("killable(%+v) should refuse", l)
		}
	}
	if os.Geteuid() != 0 {
		if killable(ports.Listener{PID: 99999, UID: os.Geteuid() + 1}) == nil {
			t.Error("processes of other users must be refused")
		}
	}
	if err := killable(ports.Listener{PID: 99999, UID: os.Geteuid()}); err != nil {
		t.Errorf("own process refused: %v", err)
	}
}
