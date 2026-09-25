package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const AppName = "portctl"

type Config struct {
	SSH      SSHConfig
	Defaults DefaultsConfig
	Tunnels  []TunnelConfig
}

type SSHConfig struct {
	User         string
	Host         string
	Port         int
	IdentityFile string
	Keepalive    KeepaliveConfig
}

type KeepaliveConfig struct {
	Enabled  bool
	Interval int
	CountMax int
}

type DefaultsConfig struct {
	RemoteHost string
}

type TunnelConfig struct {
	Port       int
	RemotePort int
	Name       string
	AutoStart  bool
	User       string
	Host       string
	Identity   string
	RemoteHost string
}

func ConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, AppName, "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", AppName, "config.yaml"), nil
}

func StateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, AppName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", AppName), nil
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yamlUnmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&c)
	if err := Validate(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Default returns the configuration used when no config file exists.
func Default() Config {
	var c Config
	applyDefaults(&c)
	return c
}

func applyDefaults(c *Config) {
	if c.SSH.Port == 0 {
		c.SSH.Port = 22
	}
	if c.Defaults.RemoteHost == "" {
		c.Defaults.RemoteHost = "127.0.0.1"
	}
}

func Validate(c Config) error {
	seen := map[int]bool{}
	for i, t := range c.Tunnels {
		if t.Port < 1 || t.Port > 65535 {
			return fmt.Errorf("tunnels[%d]: invalid port %d", i, t.Port)
		}
		if t.RemotePort != 0 && (t.RemotePort < 1 || t.RemotePort > 65535) {
			return fmt.Errorf("tunnels[%d]: invalid remote_port %d", i, t.RemotePort)
		}
		if seen[t.Port] {
			return fmt.Errorf("duplicate tunnel port %d", t.Port)
		}
		seen[t.Port] = true
		if err := checkTarget(fmt.Sprintf("tunnels[%d].", i), t.User, t.Host, t.RemoteHost); err != nil {
			return err
		}
	}
	if err := checkTarget("ssh.", c.SSH.User, c.SSH.Host, ""); err != nil {
		return err
	}
	if err := checkTarget("defaults.", "", "", c.Defaults.RemoteHost); err != nil {
		return err
	}
	if c.SSH.Port < 1 || c.SSH.Port > 65535 {
		return fmt.Errorf("invalid SSH port %d", c.SSH.Port)
	}
	if c.SSH.Keepalive.Enabled {
		if c.SSH.Keepalive.Interval <= 0 {
			return errors.New("ssh.keepalive.interval must be > 0")
		}
		if c.SSH.Keepalive.CountMax <= 0 {
			return errors.New("ssh.keepalive.count_max must be > 0")
		}
	}
	return nil
}

func FindTunnel(c Config, port int) (TunnelConfig, bool) {
	for _, t := range c.Tunnels {
		if t.Port == port {
			return t, true
		}
	}
	return TunnelConfig{}, false
}

func Merge(c Config, t TunnelConfig) Resolved {
	r := Resolved{
		Port: t.Port, RemotePort: t.RemotePort, Name: t.Name,
		User: c.SSH.User, Host: c.SSH.Host, SSHPort: c.SSH.Port,
		IdentityFile: c.SSH.IdentityFile, RemoteHost: c.Defaults.RemoteHost,
		Keepalive: c.SSH.Keepalive,
	}
	if r.RemotePort == 0 {
		r.RemotePort = r.Port
	}
	if t.User != "" {
		r.User = t.User
	}
	if t.Host != "" {
		r.Host = t.Host
	}
	if t.Identity != "" {
		r.IdentityFile = t.Identity
	}
	if t.RemoteHost != "" {
		r.RemoteHost = t.RemoteHost
	}
	return r
}

type Resolved struct {
	Port, RemotePort, SSHPort                  int
	Name, User, Host, IdentityFile, RemoteHost string
	Keepalive                                  KeepaliveConfig
}

// Validate checks the values that end up on the ssh command line, including
// those that came from command-line overrides.
func (r Resolved) Validate() error {
	if r.User == "" || r.Host == "" {
		return errors.New("SSH user and host are required (set them in config or with --user/--host)")
	}
	return checkTarget("", r.User, r.Host, r.RemoteHost)
}

// checkTarget rejects values that ssh would read as an option (a leading "-")
// or that would change the meaning of the -L forward specification. Empty
// values are skipped.
func checkTarget(prefix, user, host, remoteHost string) error {
	for _, f := range []struct{ name, v string }{{"user", user}, {"host", host}, {"remote_host", remoteHost}} {
		if f.v == "" {
			continue
		}
		if strings.HasPrefix(f.v, "-") || strings.ContainsAny(f.v, " \t\r\n") {
			return fmt.Errorf("invalid %s%s %q: must not start with '-' or contain spaces", prefix, f.name, f.v)
		}
	}
	if strings.Contains(remoteHost, ":") && !(strings.HasPrefix(remoteHost, "[") && strings.HasSuffix(remoteHost, "]")) {
		return fmt.Errorf("invalid %sremote_host %q: enclose IPv6 addresses in brackets, e.g. [::1]", prefix, remoteHost)
	}
	return nil
}

func Init(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return fmt.Errorf("configuration already exists: %s", path)
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	const example = `ssh:
  user: your-user
  host: your-server
  port: 22

defaults:
  remote_host: 127.0.0.1

tunnels:
  - port: 8000
    name: development
    auto_start: false
`
	if err := os.WriteFile(path, []byte(example), 0600); err != nil {
		return err
	}
	return nil
}
