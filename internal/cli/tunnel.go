package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oldanidavide/portctl/internal/config"
	"github.com/oldanidavide/portctl/internal/ports"
	"github.com/oldanidavide/portctl/internal/process"
	"github.com/oldanidavide/portctl/internal/sshutil"
	"github.com/oldanidavide/portctl/internal/state"
)

func (a *App) tunnelCmd(args []string) int {
	if len(args) == 0 {
		return a.list(nil)
	}
	switch args[0] {
	case "list", "ls":
		return a.list(args[1:])
	case "start":
		return a.start(args[1:])
	case "stop":
		return a.stop(args[1:])
	case "restart":
		return a.restart(args[1:])
	case "start-all":
		return a.startAll(args[1:])
	case "stop-all":
		return a.stopAll(args[1:])
	case "restart-all":
		return a.restartAll(args[1:])
	case "show":
		return a.show(args[1:])
	case "logs":
		return a.logs(args[1:])
	default:
		errf("unknown tunnel command: %s\n\nRun %s for usage.", args[0], cErr.Cmd("portctl help"))
		return 2
	}
}

// resolve combines the config entry for port (if any) with command-line
// overrides. Without a config file, the overrides must provide user and host.
func (a *App) resolve(port int, o state.Overrides) (config.Resolved, error) {
	c, exists, err := a.loadIfExists()
	if err != nil {
		return config.Resolved{}, err
	}
	r := merged(c, port, o)
	if !exists && (r.User == "" || r.Host == "") {
		return r, a.errNoConfig()
	}
	return r, r.Validate()
}

// merged resolves a tunnel without validating it, for display.
func merged(c config.Config, port int, o state.Overrides) config.Resolved {
	t, ok := config.FindTunnel(c, port)
	if !ok {
		t = config.TunnelConfig{Port: port}
	}
	r := config.Merge(c, t)
	if o.User != "" {
		r.User = o.User
	}
	if o.Host != "" {
		r.Host = o.Host
	}
	if o.Identity != "" {
		r.IdentityFile = o.Identity
	}
	return r
}

func (a *App) start(args []string) int {
	fs := newFlags("tunnel start", "<port> [options]")
	var o state.Overrides
	fs.StringVar(&o.User, "user", "", "SSH user")
	fs.StringVar(&o.User, "u", "", "SSH user (shorthand)")
	fs.StringVar(&o.Host, "host", "", "SSH host")
	fs.StringVar(&o.Host, "H", "", "SSH host (shorthand)")
	fs.StringVar(&o.Identity, "identity", "", "identity file")
	fg := fs.Bool("foreground", false, "keep ssh attached to the terminal")
	dry := fs.Bool("dry-run", false, "print the ssh command without running it")
	p, rc, ok := parsePortArgs(fs, args)
	if !ok {
		return rc
	}
	if *dry {
		r, err := a.resolve(p, o)
		if err != nil {
			return fail(err)
		}
		if *fg {
			fmt.Println(cOut.Cmd(sshutil.Join(sshutil.Args(r))))
		} else {
			fmt.Println(cOut.Cmd(sshutil.String(r)))
		}
		return 0
	}
	return a.startTunnel(p, o, *fg)
}

func (a *App) startTunnel(p int, o state.Overrides, fg bool) int {
	r, e := a.resolve(p, o)
	if e != nil {
		return fail(e)
	}
	if running, s := a.running(p); running {
		errf("Tunnel %d is already running (PID %d)", p, s.PID)
		return 1
	}
	if a.portBusy(p) {
		a.reportBusy(p)
		return 1
	}
	if fg {
		return a.foreground(r)
	}

	if err := os.MkdirAll(filepath.Join(a.StateDir, "logs"), 0700); err != nil {
		return fail(err)
	}
	_, lp := state.Paths(a.StateDir, p)
	rotateLog(lp)
	logf, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fail(err)
	}
	defer logf.Close()
	var offset int64
	if st, err := logf.Stat(); err == nil {
		offset = st.Size()
	}
	fmt.Fprintf(logf, "--- %s portctl: %s\n", time.Now().Format(time.RFC3339), sshutil.String(r))

	sshArgs := sshutil.BackgroundArgs(r)
	if !a.Quiet {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", cErr.Blue("→"), cErr.Dim("Connecting to"), cErr.Bold(fmt.Sprintf("%s@%s", r.User, r.Host))+cErr.Dim(fmt.Sprintf(" for 127.0.0.1:%d…", p)))
	}
	if err := process.RunDetaching(sshArgs, logf); err != nil {
		errf("Failed to start tunnel %d %s", p, cErr.Dim(fmt.Sprintf("(ssh exit code %d)", process.ExitCode(err))))
		printLogTail(lp, offset)
		return 1
	}
	pid, ok := findTunnelPID(sshArgs, 5*time.Second)
	if !ok {
		errf("Failed to start tunnel %d\n\nssh returned successfully but its background process could not be found.", p)
		printLogTail(lp, offset)
		return 1
	}
	s := state.State{Port: p, PID: pid, StartedAt: time.Now(), Command: sshArgs, Overrides: o}
	if err := state.Save(a.StateDir, s); err != nil {
		_ = process.Stop(pid, s.Command, 2*time.Second)
		return fail(err)
	}
	success(a.Quiet, "Tunnel started")
	out(a.Quiet, "\n  %s %s %s\n  %s %s\n  %s %d",
		cOut.Bold(fmt.Sprintf("127.0.0.1:%d", p)), cOut.Dim("→"), cOut.Bold(fmt.Sprintf("%s:%d", r.RemoteHost, r.RemotePort)),
		cOut.Label("via"), cOut.Magenta(fmt.Sprintf("%s@%s", r.User, r.Host)),
		cOut.Label("PID"), pid)
	return 0
}

// findTunnelPID locates the backgrounded ssh (forked by -f, so not our child)
// by its exact command line.
func findTunnelPID(args []string, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if pid, ok := process.Find(args); ok {
			return pid, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// printLogTail prints what ssh wrote to the log during this start attempt.
func printLogTail(path string, offset int64) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	b, _ := io.ReadAll(f)
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" && !strings.HasPrefix(l, "--- ") {
			lines = append(lines, l)
		}
	}
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	if len(lines) > 0 {
		fmt.Fprintf(os.Stderr, "\n%s\n", cErr.Bold("ssh output:"))
		for _, l := range lines {
			fmt.Fprintf(os.Stderr, "  %s %s\n", cErr.Dim("│"), cErr.Red(l))
		}
	}
	fmt.Fprintf(os.Stderr, "\n%s %s\n", cErr.Label("Full log:"), cErr.Dim(path))
}

func (a *App) foreground(r config.Resolved) int {
	fmt.Printf("%s\n\n  %s 127.0.0.1:%d\n  %s %s:%d\n  %s %s\n\n%s\n\n",
		cOut.Bold("Starting SSH tunnel..."),
		cOut.Label("Local: "), r.Port, cOut.Label("Remote:"), r.RemoteHost, r.RemotePort,
		cOut.Label("SSH:   "), cOut.Magenta(r.User+"@"+r.Host), cOut.Dim("Press Ctrl+C to stop."))
	cmd := exec.Command("ssh", sshutil.Args(r)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return process.ExitCode(err)
	}
	fmt.Println(cOut.Yellow("Tunnel stopped."))
	return 0
}

// running reports whether the tunnel on port is alive, removing stale state.
func (a *App) running(port int) (bool, state.State) {
	s, e := state.Load(a.StateDir, port)
	if e != nil {
		return false, state.State{}
	}
	if process.AliveOwned(s.PID, s.Command) {
		return true, s
	}
	state.Remove(a.StateDir, port)
	return false, state.State{}
}

// runningTunnels returns the live portctl-managed tunnels, sorted by port.
func (a *App) runningTunnels() []state.State {
	var out []state.State
	for _, s := range state.List(a.StateDir) {
		if process.AliveOwned(s.PID, s.Command) {
			out = append(out, s)
		}
	}
	return out
}

// managedByPID maps the PID of every live, portctl-managed tunnel to its state.
func (a *App) managedByPID() map[int]state.State {
	m := map[int]state.State{}
	for _, s := range a.runningTunnels() {
		m[s.PID] = s
	}
	return m
}

func (a *App) stop(args []string) int {
	p, rc, ok := parsePortArgs(newFlags("tunnel stop", "<port>"), args)
	if !ok {
		return rc
	}
	return a.stopPort(p)
}

func (a *App) stopPort(p int) int {
	s, e := state.Load(a.StateDir, p)
	if e != nil {
		out(a.Quiet, "%s", cOut.Dim(fmt.Sprintf("Tunnel %d is not running.", p)))
		return 0
	}
	if !process.AliveOwned(s.PID, s.Command) {
		state.Remove(a.StateDir, p)
		out(a.Quiet, "%s", cOut.Dim(fmt.Sprintf("Tunnel %d is not running.", p)))
		return 0
	}
	if e := process.Stop(s.PID, s.Command, 3*time.Second); e != nil {
		errf("Failed to stop tunnel %d: %v", p, e)
		return 1
	}
	state.Remove(a.StateDir, p)
	success(a.Quiet, "Tunnel %d stopped", p)
	return 0
}

func (a *App) restart(args []string) int {
	p, rc, ok := parsePortArgs(newFlags("tunnel restart", "<port>"), args)
	if !ok {
		return rc
	}
	return a.restartPort(p)
}

// restartPort stops and starts a tunnel, keeping the command-line overrides
// it was started with.
func (a *App) restartPort(p int) int {
	s, _ := state.Load(a.StateDir, p)
	if rc := a.stopPort(p); rc != 0 {
		return rc
	}
	return a.startTunnel(p, s.Overrides, false)
}

func (a *App) list(args []string) int {
	fs := newFlags("tunnel list", "[--json]")
	jsonOut := fs.Bool("json", false, "print JSON")
	if rc, ok := parseNoArgs(fs, args); !ok {
		return rc
	}
	c, exists, err := a.loadIfExists()
	if err != nil {
		return fail(err)
	}
	type row struct {
		Port   int    `json:"port"`
		Name   string `json:"name"`
		Status string `json:"status"`
		PID    int    `json:"pid,omitempty"`
		Remote string `json:"remote"`
	}
	var rows []row
	add := func(r config.Resolved, pid int) {
		st := "stopped"
		if pid > 0 {
			st = "running"
		}
		rows = append(rows, row{r.Port, r.Name, st, pid, fmt.Sprintf("%s → %s:%d", r.Host, r.RemoteHost, r.RemotePort)})
	}
	running := a.runningTunnels()
	byPort := map[int]state.State{}
	for _, s := range running {
		byPort[s.Port] = s
	}
	for _, t := range c.Tunnels {
		s := byPort[t.Port]
		add(merged(c, t.Port, s.Overrides), s.PID)
	}
	// Tunnels started with --user/--host for ports that are not configured.
	for _, s := range running {
		if _, configured := config.FindTunnel(c, s.Port); !configured {
			add(merged(c, s.Port, s.Overrides), s.PID)
		}
	}
	if !exists && len(rows) == 0 {
		return fail(a.errNoConfig())
	}
	if *jsonOut {
		if rows == nil {
			rows = []row{}
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Println(cOut.Header(fmt.Sprintf("%-6s %-14s %-9s %-7s %s", "PORT", "NAME", "STATUS", "PID", "REMOTE")))
	for _, x := range rows {
		pid := "-"
		if x.PID > 0 {
			pid = strconv.Itoa(x.PID)
		}
		status := cOut.Dim(fmt.Sprintf("%-9s", "○ "+x.Status))
		if x.Status == "running" {
			status = cOut.Green(fmt.Sprintf("%-9s", "● "+x.Status))
		}
		fmt.Printf("%s %s %s %s %s\n", cOut.Bold(fmt.Sprintf("%-6d", x.Port)), fmt.Sprintf("%-14s", truncate(x.Name, 14)), status,
			cOut.Dim(fmt.Sprintf("%-7s", pid)), cOut.Magenta(x.Remote))
	}
	return 0
}

func (a *App) show(args []string) int {
	p, rc, ok := parsePortArgs(newFlags("tunnel show", "<port>"), args)
	if !ok {
		return rc
	}
	running, s := a.running(p)
	r, e := a.resolve(p, s.Overrides)
	if e != nil {
		return fail(e)
	}
	status := cOut.Dim("○ stopped")
	pid := cOut.Dim("-")
	started := cOut.Dim("-")
	if running {
		status = cOut.BoldGreen("● running")
		pid = strconv.Itoa(s.PID)
		started = s.StartedAt.Local().Format("2006-01-02 15:04:05")
	}
	name := r.Name
	if name == "" {
		name = cOut.Dim("(unnamed)")
	}
	field := func(label, value string) { fmt.Printf("  %s %s\n", cOut.Label(fmt.Sprintf("%-8s", label)), value) }
	fmt.Printf("%s %s\n\n", cOut.Bold("Tunnel"), cOut.Bold(name))
	field("Status", status)
	field("Local", cOut.Bold(fmt.Sprintf("127.0.0.1:%d", r.Port)))
	field("Remote", fmt.Sprintf("%s:%d", r.RemoteHost, r.RemotePort))
	field("SSH", cOut.Magenta(fmt.Sprintf("%s@%s:%d", r.User, r.Host, r.SSHPort)))
	field("PID", pid)
	field("Started", started)
	field("Command", cOut.Cmd(sshutil.String(r)))
	return 0
}

func (a *App) logs(args []string) int {
	p, rc, ok := parsePortArgs(newFlags("tunnel logs", "<port>"), args)
	if !ok {
		return rc
	}
	_, lp := state.Paths(a.StateDir, p)
	b, e := os.ReadFile(lp)
	if os.IsNotExist(e) {
		errf("No logs for tunnel %d.", p)
		return 1
	}
	if e != nil {
		return fail(e)
	}
	fmt.Print(string(b))
	return 0
}

func (a *App) startAll(args []string) int {
	if rc, ok := parseNoArgs(newFlags("tunnel start-all", ""), args); !ok {
		return rc
	}
	c, e := a.load()
	if e != nil {
		return fail(e)
	}
	rc := 0
	for _, t := range c.Tunnels {
		if t.AutoStart {
			if x := a.startTunnel(t.Port, state.Overrides{}, false); x != 0 {
				rc = x
			}
		}
	}
	return rc
}

func (a *App) stopAll(args []string) int {
	if rc, ok := parseNoArgs(newFlags("tunnel stop-all", ""), args); !ok {
		return rc
	}
	rc := 0
	for _, s := range state.List(a.StateDir) {
		if x := a.stopPort(s.Port); x != 0 {
			rc = x
		}
	}
	return rc
}

func (a *App) restartAll(args []string) int {
	if rc, ok := parseNoArgs(newFlags("tunnel restart-all", ""), args); !ok {
		return rc
	}
	live := a.runningTunnels()
	if len(live) == 0 {
		out(a.Quiet, "%s", cOut.Dim("No tunnels are running."))
		return 0
	}
	rc := 0
	for _, s := range live {
		if x := a.restartPort(s.Port); x != 0 {
			rc = x
		}
	}
	return rc
}

// portBusy reports whether anything already listens on the port. Besides the
// bind test on 127.0.0.1 it asks lsof, because on macOS a wildcard (*:port)
// listener does not always make a 127.0.0.1 bind fail.
func (a *App) portBusy(port int) bool {
	if !portFree(port) {
		return true
	}
	ls, err := ports.ByPort(port)
	return err == nil && len(ls) > 0
}

// reportBusy explains which process holds a port that a tunnel needs.
func (a *App) reportBusy(p int) {
	errf("Cannot start tunnel %d", p)
	busy := cErr.Yellow(fmt.Sprintf("Port %d is already in use", p))
	ls, err := ports.ByPort(p)
	if err != nil || len(ls) == 0 {
		fmt.Fprintf(os.Stderr, "\n  %s.\n  The owning process is not visible to you (it may belong to another user).\n  Try: %s\n", busy, cErr.Cmd(fmt.Sprintf("sudo portctl port info %d", p)))
		return
	}
	fmt.Fprintf(os.Stderr, "\n  %s by:\n", busy)
	managed := a.managedByPID()
	for _, g := range ports.Owners(ls) {
		fmt.Fprintf(os.Stderr, "\n%s", describeOwner(cErr, g, managed, "    "))
	}
	fmt.Fprintf(os.Stderr, "\n  Free it with: %s\n", cErr.Cmd(fmt.Sprintf("portctl port kill %d", p)))
}

func portFree(port int) bool {
	l, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		return !errors.Is(e, syscall.EADDRINUSE)
	}
	_ = l.Close()
	return true
}

func rotateLog(path string) {
	st, e := os.Stat(path)
	if e == nil && st.Size() > 5*1024*1024 {
		old := path + ".1"
		_ = os.Remove(old)
		_ = os.Rename(path, old)
	}
}
