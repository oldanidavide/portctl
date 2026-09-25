package sshutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/oldanidavide/portctl/internal/config"
)

// Args returns the ssh arguments for a tunnel that stays attached to the caller.
// ExitOnForwardFailure makes ssh fail instead of silently running without the
// forward, and ControlPath=none keeps the tunnel in its own ssh process so it
// can be tracked by PID even when the user's ssh config enables multiplexing.
func Args(r config.Resolved) []string {
	args := []string{"-N", "-L", fmt.Sprintf("127.0.0.1:%d:%s:%d", r.Port, r.RemoteHost, r.RemotePort),
		"-o", "ExitOnForwardFailure=yes", "-o", "ControlPath=none"}
	if r.SSHPort != 22 {
		args = append(args, "-p", strconv.Itoa(r.SSHPort))
	}
	if r.IdentityFile != "" {
		identity := filepath.Clean(r.IdentityFile)
		if len(identity) >= 2 && identity[0] == '~' && (identity[1] == '/' || identity[1] == filepath.Separator) {
			if home, err := os.UserHomeDir(); err == nil {
				identity = filepath.Join(home, identity[2:])
			}
		}
		args = append(args, "-i", identity)
	}
	if r.Keepalive.Enabled {
		args = append(args, "-o", "ServerAliveInterval="+strconv.Itoa(r.Keepalive.Interval), "-o", "ServerAliveCountMax="+strconv.Itoa(r.Keepalive.CountMax))
	}
	target := r.Host
	if r.User != "" {
		target = r.User + "@" + target
	}
	args = append(args, target)
	return args
}

// BackgroundArgs makes ssh authenticate in the foreground (so password and
// host-key prompts work on the terminal) and fork into the background only once
// the connection and port forward are established.
func BackgroundArgs(r config.Resolved) []string {
	return append([]string{"-f"}, Args(r)...)
}

func String(r config.Resolved) string { return Join(BackgroundArgs(r)) }

// Join renders ssh arguments as a copy-pasteable command line.
func Join(a []string) string {
	out := "ssh"
	for _, v := range a {
		out += " " + shellQuote(v)
	}
	return out
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':' || c == '/' || c == '@' || c == '=') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	out := "'"
	for _, c := range s {
		if c == '\'' {
			out += "'\\''"
		} else {
			out += string(c)
		}
	}
	return out + "'"
}
