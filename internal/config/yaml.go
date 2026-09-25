package config

import (
	"fmt"
	"strconv"
	"strings"
)

// The config file intentionally uses a small, dependency-free YAML subset:
// mappings, sequences, scalar strings, integers and booleans. This is enough
// for the documented schema and keeps the binary free of third-party runtime
// dependencies.
func yamlUnmarshal(data []byte, out *Config) error {
	section := ""
	var current *TunnelConfig
	keepalive := -1 // indentation of the "keepalive:" line while inside that block
	for n, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		lineNo := n + 1
		s := strings.TrimSpace(stripComment(raw))
		if s == "" {
			continue
		}
		trimmed := strings.TrimLeft(raw, " ")
		if strings.HasPrefix(trimmed, "\t") {
			return fmt.Errorf("line %d: use spaces, not tabs, for indentation", lineNo)
		}
		indent := len(raw) - len(trimmed)
		if indent%2 != 0 {
			return fmt.Errorf("line %d: indentation must use multiples of 2 spaces", lineNo)
		}
		if keepalive >= 0 && indent <= keepalive {
			keepalive = -1
		}
		if indent == 0 {
			k, v, ok := splitKV(s)
			if !ok || v != "" {
				return fmt.Errorf("line %d: expected a section (ssh:, defaults: or tunnels:)", lineNo)
			}
			switch k {
			case "ssh", "defaults", "tunnels":
				section, current = k, nil
			default:
				return fmt.Errorf("line %d: unknown section %q", lineNo, k)
			}
			continue
		}
		if section == "tunnels" && (s == "-" || strings.HasPrefix(s, "- ")) {
			out.Tunnels = append(out.Tunnels, TunnelConfig{})
			current = &out.Tunnels[len(out.Tunnels)-1]
			if s = strings.TrimSpace(s[1:]); s == "" {
				continue
			}
		}
		k, v, ok := splitKV(s)
		if !ok {
			return fmt.Errorf("line %d: expected key: value", lineNo)
		}
		switch section {
		case "ssh":
			if k == "keepalive" && v == "" && keepalive < 0 {
				keepalive = indent
				continue
			}
			if err := setSSH(&out.SSH, k, v, keepalive >= 0, lineNo); err != nil {
				return err
			}
		case "defaults":
			switch k {
			case "remote_host":
				out.Defaults.RemoteHost = scalar(v)
			default:
				return fmt.Errorf("line %d: unknown defaults field %q", lineNo, k)
			}
		case "tunnels":
			if current == nil {
				return fmt.Errorf("line %d: tunnel field without a list item", lineNo)
			}
			if err := setTunnel(current, k, v, lineNo); err != nil {
				return err
			}
		default:
			return fmt.Errorf("line %d: field outside a section", lineNo)
		}
	}
	return nil
}

// stripComment removes a "# comment" that starts the line or follows a
// space, unless it is inside a quoted value.
func stripComment(s string) string {
	var quote rune
	for i, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case (c == '"' || c == '\'') && (i == 0 || s[i-1] == ' '):
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return s[:i]
		}
	}
	return s
}

func splitKV(s string) (string, string, bool) {
	i := strings.IndexByte(s, ':')
	if i < 1 {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}
func scalar(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}
func integer(v string, line int) (int, error) {
	n, e := strconv.Atoi(scalar(v))
	if e != nil {
		return 0, fmt.Errorf("line %d: invalid integer %q", line, v)
	}
	return n, nil
}
func boolean(v string, line int) (bool, error) {
	switch strings.ToLower(scalar(v)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("line %d: expected true or false, got %q", line, v)
	}
}
func setSSH(c *SSHConfig, k, v string, ka bool, line int) error {
	if ka {
		switch k {
		case "enabled":
			x, e := boolean(v, line)
			c.Keepalive.Enabled = x
			return e
		case "interval":
			x, e := integer(v, line)
			c.Keepalive.Interval = x
			return e
		case "count_max":
			x, e := integer(v, line)
			c.Keepalive.CountMax = x
			return e
		default:
			return fmt.Errorf("line %d: unknown keepalive field %q", line, k)
		}
	}
	switch k {
	case "user":
		c.User = scalar(v)
	case "host":
		c.Host = scalar(v)
	case "port":
		x, e := integer(v, line)
		if e != nil {
			return e
		}
		c.Port = x
	case "identity_file":
		c.IdentityFile = scalar(v)
	case "password":
		return fmt.Errorf("line %d: ssh.password is not supported; remove it — ssh asks for the password on the terminal when the tunnel starts (or set up a key with ssh-copy-id)", line)
	default:
		return fmt.Errorf("line %d: unknown ssh field %q", line, k)
	}
	return nil
}
func setTunnel(c *TunnelConfig, k, v string, line int) error {
	switch k {
	case "port":
		x, e := integer(v, line)
		if e != nil {
			return e
		}
		c.Port = x
	case "remote_port":
		x, e := integer(v, line)
		if e != nil {
			return e
		}
		c.RemotePort = x
	case "name":
		c.Name = scalar(v)
	case "auto_start":
		x, e := boolean(v, line)
		if e != nil {
			return e
		}
		c.AutoStart = x
	case "user":
		c.User = scalar(v)
	case "host":
		c.Host = scalar(v)
	case "identity_file":
		c.Identity = scalar(v)
	case "remote_host":
		c.RemoteHost = scalar(v)
	default:
		return fmt.Errorf("line %d: unknown tunnel field %q", line, k)
	}
	return nil
}
