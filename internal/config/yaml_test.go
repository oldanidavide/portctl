package config

import (
	"strings"
	"testing"
)

func TestYAMLFullExample(t *testing.T) {
	in := `# portctl config
ssh:
  user: davide   # inline comment
  host: "prod # not a comment"
  port: 2222
  keepalive:
    enabled: true
    interval: 30
    count_max: 3
  identity_file: ~/.ssh/id_ed25519

defaults:
  remote_host: 127.0.0.1

tunnels:
  - port: 8000
    name: development
    auto_start: true
  -
    port: 3306
    remote_port: 3307
    name:
    host: db
`
	var c Config
	if err := yamlUnmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	if c.SSH.User != "davide" || c.SSH.Host != "prod # not a comment" || c.SSH.Port != 2222 {
		t.Errorf("ssh = %+v", c.SSH)
	}
	if c.SSH.IdentityFile != "~/.ssh/id_ed25519" {
		t.Errorf("identity_file after keepalive block not parsed: %+v", c.SSH)
	}
	if k := c.SSH.Keepalive; !k.Enabled || k.Interval != 30 || k.CountMax != 3 {
		t.Errorf("keepalive = %+v", k)
	}
	if len(c.Tunnels) != 2 {
		t.Fatalf("tunnels = %+v", c.Tunnels)
	}
	if tn := c.Tunnels[0]; tn.Port != 8000 || tn.Name != "development" || !tn.AutoStart {
		t.Errorf("tunnel 0 = %+v", tn)
	}
	if tn := c.Tunnels[1]; tn.Port != 3306 || tn.RemotePort != 3307 || tn.Name != "" || tn.Host != "db" {
		t.Errorf("tunnel 1 = %+v", tn)
	}
}

func TestYAMLErrors(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ssh:\n   user: u\n", "multiples of 2"},
		{"ssh:\n\tuser: u\n", "tabs"},
		{"servers:\n", "unknown section"},
		{"ssh: x\n", "expected a section"},
		{"user: u\n", "expected a section"},
		{"ssh:\n  nope: 1\n", "unknown ssh field"},
		{"ssh:\n  keepalive:\n    nope: 1\n", "unknown keepalive field"},
		{"defaults:\n  keepalive:\n", "unknown defaults field"},
		{"tunnels:\n  port: 1\n", "without a list item"},
		{"tunnels:\n  - port: x\n", "invalid integer"},
		{"tunnels:\n  - auto_start: yes\n", "expected true or false"},
	} {
		var cfg Config
		err := yamlUnmarshal([]byte(c.in), &cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want error containing %q", c.in, err, c.want)
		}
	}
}

func TestPasswordRejectedWithHint(t *testing.T) {
	var c Config
	err := yamlUnmarshal([]byte("ssh:\n  user: u\n  host: h\n  password: secret\n"), &c)
	if err == nil || !strings.Contains(err.Error(), "ssh.password is not supported") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("error must not echo the password")
	}
}
