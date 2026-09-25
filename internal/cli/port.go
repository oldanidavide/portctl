package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oldanidavide/portctl/internal/config"
	"github.com/oldanidavide/portctl/internal/ports"
	"github.com/oldanidavide/portctl/internal/process"
	"github.com/oldanidavide/portctl/internal/state"
	"github.com/oldanidavide/portctl/internal/ui"
)

func (a *App) portCmd(args []string) int {
	if len(args) == 0 {
		return a.portList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return a.portList(args[1:])
	case "info":
		return a.portInfo(args[1:])
	case "kill":
		return a.portKill(args[1:])
	default:
		errf("unknown port command: %s\n\nRun %s for usage.", args[0], cErr.Cmd("portctl help"))
		return 2
	}
}

// tunnelLabel names the managed tunnel a PID belongs to, or "" if none.
func tunnelLabel(pid int, managed map[int]state.State, c config.Config) string {
	s, ok := managed[pid]
	if !ok {
		return ""
	}
	if t, ok := config.FindTunnel(c, s.Port); ok && t.Name != "" {
		return t.Name
	}
	return "tunnel " + strconv.Itoa(s.Port)
}

func (a *App) portList(args []string) int {
	fs := newFlags("port list", "[--json]")
	jsonOut := fs.Bool("json", false, "print JSON")
	if rc, ok := parseNoArgs(fs, args); !ok {
		return rc
	}
	ls, err := ports.List()
	if err != nil {
		return fail(err)
	}
	managed := a.managedByPID()
	c := a.loadOptional()

	type row struct {
		Port    int    `json:"port"`
		Addr    string `json:"addr"`
		PID     int    `json:"pid,omitempty"`
		User    string `json:"user,omitempty"`
		Process string `json:"process,omitempty"`
		Tunnel  string `json:"tunnel,omitempty"`
	}
	var rows []row
	// One row per (port, pid), with all its addresses joined.
	for i := 0; i < len(ls); {
		j := i
		for j < len(ls) && ls[j].Port == ls[i].Port {
			j++
		}
		for _, g := range ports.Owners(ls[i:j]) {
			l := g[0]
			rows = append(rows, row{l.Port, ports.Addrs(g), l.PID, l.User, l.Name, tunnelLabel(l.PID, managed, c)})
		}
		i = j
	}

	if *jsonOut {
		if rows == nil {
			rows = []row{}
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(rows) == 0 {
		fmt.Println(cOut.Dim("No listening TCP ports visible to you."))
		return 0
	}
	fmt.Println(cOut.Header(fmt.Sprintf("%-6s %-22s %-7s %-12s %-20s %s", "PORT", "ADDRESS", "PID", "USER", "PROCESS", "TUNNEL")))
	for _, r := range rows {
		pid, user, proc := "-", r.User, r.Process
		if r.PID > 0 {
			pid = strconv.Itoa(r.PID)
		}
		if user == "" {
			user = "?"
		}
		if proc == "" {
			proc = "?"
		}
		addr := fmt.Sprintf("%-22s", truncate(r.Addr, 22))
		if exposed(r.Addr) {
			addr = cOut.Yellow(addr) // reachable from other hosts
		} else {
			addr = cOut.Dim(addr)
		}
		tunnel := ""
		if r.Tunnel != "" {
			tunnel = cOut.Green("⇄ " + r.Tunnel)
		}
		fmt.Printf("%s %s %s %s %s %s\n", cOut.Bold(fmt.Sprintf("%-6d", r.Port)), addr, fmt.Sprintf("%-7s", pid),
			cOut.Dim(fmt.Sprintf("%-12s", truncate(user, 12))), cOut.Cyan(fmt.Sprintf("%-20s", truncate(proc, 20))), tunnel)
	}
	if os.Geteuid() != 0 {
		fmt.Printf("\n%s\n", cOut.Dim("Only your own processes are shown; run with sudo to see all. Yellow addresses are reachable from the network."))
	}
	return 0
}

func (a *App) portInfo(args []string) int {
	p, rc, ok := parsePortArgs(newFlags("port info", "<port>"), args)
	if !ok {
		return rc
	}
	ls, err := ports.ByPort(p)
	if err != nil {
		return fail(err)
	}
	c := a.loadOptional()
	t, configured := config.FindTunnel(c, p)
	if len(ls) == 0 {
		if !portFree(p) {
			fmt.Printf("%s, but the owning process is not visible to you.\nTry: %s\n", cOut.Yellow(fmt.Sprintf("Port %d is in use", p)), cOut.Cmd(fmt.Sprintf("sudo portctl port info %d", p)))
			return 1
		}
		fmt.Printf("%s Nothing is listening on port %s.\n", cOut.Green("○"), cOut.Bold(strconv.Itoa(p)))
		if configured {
			fmt.Printf("It is configured as tunnel %s (stopped). Start it with: %s\n", cOut.Bold(t.Name), cOut.Cmd(fmt.Sprintf("portctl tunnel start %d", p)))
		}
		return 0
	}
	managed := a.managedByPID()
	fmt.Printf("%s %s\n", cOut.Red("●"), cOut.Bold(fmt.Sprintf("Port %d", p)))
	for _, g := range ports.Owners(ls) {
		fmt.Printf("\n%s", describeOwner(cOut, g, managed, "  "))
		if g[0].PID > 0 {
			if st, err := process.StartTime(g[0].PID); err == nil {
				fmt.Printf("  %s %s\n", cOut.Label("Started:"), st)
			}
		}
	}
	if configured {
		if _, ok := managed[ls[0].PID]; !ok {
			fmt.Println()
			warnf("Port %d is configured for tunnel %q, but it is held by another process.", p, t.Name)
		}
	}
	return 0
}

// describeOwner renders one process holding a port (a group of listeners that
// share a PID), indented by prefix.
func describeOwner(c ui.Style, g []ports.Listener, managed map[int]state.State, prefix string) string {
	l := g[0]
	var b strings.Builder
	field := func(label, value string) { fmt.Fprintf(&b, "%s%s %s\n", prefix, c.Label(label), value) }
	if l.PID == 0 {
		field("Process:", c.Yellow("(not visible — owned by another user)"))
		field("Listen: ", ports.Addrs(g))
		return b.String()
	}
	user := l.User
	if user == "" {
		user = "?"
	}
	field("Process:", fmt.Sprintf("%s %s", c.BoldRed(l.Name), c.Dim(fmt.Sprintf("(PID %d, user %s)", l.PID, user))))
	field("Listen: ", ports.Addrs(g))
	if cmd, _ := process.CommandLine(l.PID); cmd != "" {
		field("Command:", c.Cyan(cmd))
	}
	if s, ok := managed[l.PID]; ok {
		field("Tunnel: ", c.Green(fmt.Sprintf("managed by portctl (tunnel %d)", s.Port)))
	} else if strings.HasPrefix(l.Name, "ssh") {
		field("Tunnel: ", c.Yellow("ssh process not managed by portctl"))
	}
	return b.String()
}

// exposed reports whether a listen address accepts connections from other hosts.
func exposed(addr string) bool {
	for _, a := range strings.Split(addr, ", ") {
		if a != "127.0.0.1" && a != "[::1]" && a != "localhost" {
			return true
		}
	}
	return false
}

func (a *App) portKill(args []string) int {
	fs := newFlags("port kill", "<port> [--yes] [--force]")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	fs.BoolVar(yes, "y", false, "do not ask for confirmation")
	force := fs.Bool("force", false, "send SIGKILL if the process ignores SIGTERM")
	fs.BoolVar(force, "f", false, "send SIGKILL if the process ignores SIGTERM")
	p, rc, ok := parsePortArgs(fs, args)
	if !ok {
		return rc
	}

	ls, err := ports.ByPort(p)
	if err != nil {
		return fail(err)
	}
	if len(ls) == 0 {
		if !portFree(p) {
			errf("Port %d is in use, but the owning process is not visible to you.\n  Inspect it with: %s", p, cErr.Cmd(fmt.Sprintf("sudo portctl port info %d", p)))
			return 1
		}
		out(a.Quiet, "%s Nothing is listening on port %d.", cOut.Green("○"), p)
		return 0
	}

	managed := a.managedByPID()
	rc = 0
	for _, g := range ports.Owners(ls) {
		if x := a.killOwner(p, g, managed, *yes, *force); x != 0 {
			rc = x
		}
	}
	return rc
}

func (a *App) killOwner(port int, g []ports.Listener, managed map[int]state.State, yes, force bool) int {
	l := g[0]
	pid := l.PID

	if s, ok := managed[pid]; ok {
		out(a.Quiet, "%s Port %d is held by portctl tunnel %d; stopping it.", cOut.Blue("→"), port, s.Port)
		return a.stopPort(s.Port)
	}
	if err := killable(l); err != nil {
		return fail(err)
	}

	// Identity token captured while the PID is verified to own the port;
	// from here on only that exact process is ever signalled.
	started, err := process.StartTime(pid)
	if err != nil {
		return fail(fmt.Errorf("cannot inspect PID %d: %w", pid, err))
	}

	fmt.Printf("%s %s\n\n%s\n", cOut.Red("●"), cOut.Bold(fmt.Sprintf("Port %d is held by:", port)), describeOwner(cOut, g, managed, "  "))
	if !yes {
		ok, err := confirm(fmt.Sprintf("%s Terminate %s (PID %d)? %s ", cOut.Yellow("?"), cOut.Bold(l.Name), pid, cOut.Dim("[y/N]")))
		if err != nil {
			return fail(err)
		}
		if !ok {
			fmt.Println(cOut.Yellow("Aborted."))
			return 1
		}
	}

	// Re-check right before signalling that the PID still owns the port.
	if !stillListening(port, pid) {
		out(a.Quiet, "%s", cOut.Dim(fmt.Sprintf("PID %d no longer listens on port %d; nothing to do.", pid, port)))
		return 0
	}
	sameProcess := func() bool {
		st, err := process.StartTime(pid)
		return err == nil && st == started
	}
	err = process.Terminate(pid, sameProcess, 3*time.Second, force)
	if errors.Is(err, process.ErrStillRunning) {
		errf("%s (PID %d) is still running after SIGTERM.\n  Retry with: %s", l.Name, pid, cErr.Cmd(fmt.Sprintf("portctl port kill %d --force", port)))
		return 1
	}
	if err != nil {
		return fail(fmt.Errorf("failed to terminate PID %d: %w", pid, err))
	}
	success(a.Quiet, "%s (PID %d) terminated; port %d is free", l.Name, pid, port)
	return 0
}

// killable refuses processes that must never be signalled from here.
func killable(l ports.Listener) error {
	switch {
	case l.PID == 0:
		return fmt.Errorf("the owning process is not visible to you (it may belong to another user)\n  Inspect it with: %s", cErr.Cmd("sudo portctl port info "+strconv.Itoa(l.Port)))
	case l.PID <= 1:
		return fmt.Errorf("refusing to terminate PID %d", l.PID)
	case l.PID == os.Getpid() || l.PID == os.Getppid():
		return fmt.Errorf("refusing to terminate PID %d: it is portctl itself or its parent shell", l.PID)
	case os.Geteuid() != 0 && l.UID >= 0 && l.UID != os.Geteuid():
		return fmt.Errorf("PID %d (%s) belongs to user %s\n  Re-run with sudo if you really mean it.", l.PID, l.Name, l.User)
	}
	return nil
}

func stillListening(port, pid int) bool {
	ls, err := ports.ByPort(port)
	if err != nil {
		return false
	}
	for _, l := range ls {
		if l.PID == pid {
			return true
		}
	}
	return false
}

func confirm(prompt string) (bool, error) {
	if st, err := os.Stdin.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false, errors.New("stdin is not a terminal; pass --yes to confirm non-interactively")
	}
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
