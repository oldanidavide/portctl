package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeDefaultsAndOverrides(t *testing.T) {
	c := Config{SSH: SSHConfig{User: "global", Host: "globalhost", Port: 22, IdentityFile: "globalkey"}, Defaults: DefaultsConfig{RemoteHost: "127.0.0.1"}}
	r := Merge(c, TunnelConfig{Port: 8000, User: "local", Host: "localhost", Identity: "localkey"})
	if r.User != "local" || r.Host != "localhost" || r.IdentityFile != "localkey" || r.RemotePort != 8000 {
		t.Fatalf("bad merge: %+v", r)
	}
}

func TestValidateDuplicate(t *testing.T) {
	if err := Validate(Config{Tunnels: []TunnelConfig{{Port: 1}, {Port: 1}}, SSH: SSHConfig{Port: 22}}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestValidateRejectsOptionInjection(t *testing.T) {
	for _, c := range []Config{
		{SSH: SSHConfig{Port: 22, Host: "-oProxyCommand=touch /tmp/x"}},
		{SSH: SSHConfig{Port: 22, User: "-l"}},
		{SSH: SSHConfig{Port: 22}, Tunnels: []TunnelConfig{{Port: 80, Host: "a b"}}},
		{SSH: SSHConfig{Port: 22}, Defaults: DefaultsConfig{RemoteHost: "::1"}},
		{SSH: SSHConfig{Port: 22}, Tunnels: []TunnelConfig{{Port: 80, RemoteHost: "db:5432"}}},
	} {
		if err := Validate(c); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
	ok := Config{SSH: SSHConfig{Port: 22, User: "u", Host: "h"}, Defaults: DefaultsConfig{RemoteHost: "[::1]"}}
	if err := Validate(ok); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolvedValidate(t *testing.T) {
	if err := (Resolved{User: "u"}).Validate(); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("missing host: got %v", err)
	}
	if err := (Resolved{User: "u", Host: "-oX", RemoteHost: "127.0.0.1"}).Validate(); err == nil {
		t.Error("expected error for host starting with '-'")
	}
	if err := (Resolved{User: "u", Host: "h", RemoteHost: "127.0.0.1"}).Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("ssh:\n  user: u\n  host: h\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.SSH.Port != 22 || c.Defaults.RemoteHost != "127.0.0.1" {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if d := Default(); d.SSH.Port != 22 || d.Defaults.RemoteHost != "127.0.0.1" {
		t.Fatalf("Default() = %+v", d)
	}
}

func TestInitWritesLoadableConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "portctl", "config.yaml")
	if err := Init(p); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if err := Init(p); err == nil {
		t.Fatal("Init must not overwrite an existing config")
	}
}
