package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oldanidavide/portctl/internal/ports"
	"github.com/oldanidavide/portctl/internal/state"
)

// Shell completion is dynamic: the scripts printed by "portctl completion"
// run "portctl __complete <words...> <current word>", which prints one
// candidate per line as "value<TAB>description". A final ":file" line asks
// the shell to complete file names instead. The command never fails and
// never writes to stderr, so a broken config only means fewer candidates.

// argKind says what a positional argument or a flag value is.
type argKind int

const (
	argNone       argKind = iota
	argConfigured         // tunnel ports from the config
	argRunning            // running tunnel ports
	argKnown              // configured or running tunnel ports
	argListening          // ports with a listening process
	argShell
	argUser
	argHost
	argFile
)

type flagSpec struct {
	long, short, desc string
	value             argKind // argNone for boolean flags
}

type cmdSpec struct {
	name, desc string
	aliases    []string
	sub        []cmdSpec
	flags      []flagSpec
	arg        argKind
}

var jsonFlag = flagSpec{long: "json", desc: "print JSON"}

var globalFlags = []flagSpec{
	{long: "quiet", short: "q", desc: "suppress success messages"},
	{long: "no-color", desc: "disable colors"},
}

// commandTree mirrors the commands and flags dispatched by Run.
var commandTree = cmdSpec{sub: []cmdSpec{
	{name: "tunnel", desc: "manage SSH tunnels", sub: []cmdSpec{
		{name: "list", desc: "list configured and running tunnels", aliases: []string{"ls"}, flags: []flagSpec{jsonFlag}},
		{name: "start", desc: "start a tunnel", arg: argConfigured, flags: []flagSpec{
			{long: "user", short: "u", desc: "SSH user", value: argUser},
			{long: "host", short: "H", desc: "SSH host", value: argHost},
			{long: "identity", desc: "identity file", value: argFile},
			{long: "foreground", desc: "keep ssh attached to the terminal"},
			{long: "dry-run", desc: "print the ssh command without running it"},
		}},
		{name: "stop", desc: "stop a tunnel", arg: argRunning},
		{name: "restart", desc: "restart a tunnel", arg: argRunning},
		{name: "start-all", desc: "start auto_start tunnels"},
		{name: "stop-all", desc: "stop all managed tunnels"},
		{name: "restart-all", desc: "restart running tunnels"},
		{name: "show", desc: "show tunnel details", arg: argKnown},
		{name: "logs", desc: "show tunnel log", arg: argKnown},
	}},
	{name: "port", desc: "inspect and free local ports", sub: []cmdSpec{
		{name: "list", desc: "list listening TCP ports", aliases: []string{"ls"}, flags: []flagSpec{jsonFlag}},
		{name: "info", desc: "show the process listening on a port", arg: argListening},
		{name: "kill", desc: "terminate the process listening on a port", arg: argListening, flags: []flagSpec{
			{long: "yes", short: "y", desc: "do not ask for confirmation"},
			{long: "force", short: "f", desc: "send SIGKILL if the process ignores SIGTERM"},
		}},
	}},
	{name: "config", desc: "show or create the configuration", sub: []cmdSpec{
		{name: "path", desc: "print the configuration path"},
		{name: "init", desc: "create an example configuration"},
	}},
	{name: "completion", desc: "print shell completion", arg: argShell},
	{name: "help", desc: "show usage"},
	{name: "version", desc: "print version"},
}}

type candidate struct{ value, desc string }

// listenerTimeout bounds lsof/ss so that a slow system never blocks the shell.
const listenerTimeout = time.Second

func (a *App) completeCmd(args []string) int {
	cands, files := a.complete(args)
	w := bufio.NewWriter(os.Stdout)
	for _, c := range cands {
		if c.desc != "" {
			fmt.Fprintf(w, "%s\t%s\n", c.value, c.desc)
		} else {
			fmt.Fprintln(w, c.value)
		}
	}
	if files {
		fmt.Fprintln(w, ":file")
	}
	w.Flush()
	return 0
}

// complete returns the candidates for the last word of words, which is the
// (possibly empty) word being typed; the others are the words before it.
// files reports that file names should be completed.
func (a *App) complete(words []string) (cands []candidate, files bool) {
	if len(words) == 0 {
		words = []string{""}
	}
	cur, words := words[len(words)-1], words[:len(words)-1]

	i := 0
	for i < len(words) && findFlag(globalFlags, words[i]) != nil {
		i++
	}
	node := &commandTree
	for ; node.sub != nil && i < len(words); i++ {
		if node = node.child(words[i]); node == nil {
			return nil, false
		}
	}
	if node.sub != nil {
		if node == &commandTree && strings.HasPrefix(cur, "-") {
			return flagCandidates(globalFlags, nil, cur), false
		}
		for _, c := range node.sub {
			cands = append(cands, candidate{c.name, c.desc})
		}
		return filterPrefix(cands, cur), false
	}

	used := map[*flagSpec]bool{}
	var pending *flagSpec
	npos := 0
	for _, w := range words[i:] {
		switch {
		case w == "=": // bash splits --flag=value at "="
		case pending != nil:
			pending = nil
		case strings.HasPrefix(w, "-") && w != "-":
			if f := findFlag(node.flags, w); f != nil {
				used[f] = true
				if f.value != argNone && !strings.Contains(w, "=") {
					pending = f
				}
			}
		default:
			npos++
		}
	}
	if pending != nil {
		return a.values(pending.value, cur)
	}
	if strings.HasPrefix(cur, "-") {
		return flagCandidates(node.flags, used, cur), false
	}
	if npos == 0 && node.arg != argNone {
		return a.values(node.arg, cur)
	}
	if cur == "" {
		return flagCandidates(node.flags, used, cur), false
	}
	return nil, false
}

func (c *cmdSpec) child(name string) *cmdSpec {
	for i := range c.sub {
		s := &c.sub[i]
		if s.name == name {
			return s
		}
		for _, a := range s.aliases {
			if a == name {
				return s
			}
		}
	}
	return nil
}

// findFlag returns the flag named by word ("-u", "--user", "-user" or
// "--user=x"), as the flag package accepts them.
func findFlag(flags []flagSpec, word string) *flagSpec {
	if !strings.HasPrefix(word, "-") {
		return nil
	}
	name, _, _ := strings.Cut(strings.TrimLeft(word, "-"), "=")
	for i := range flags {
		if f := &flags[i]; name != "" && (name == f.long || name == f.short) {
			return f
		}
	}
	return nil
}

// flagCandidates offers the long form of each unused flag, or the short
// forms when the word being typed is a single-dash prefix of one.
func flagCandidates(flags []flagSpec, used map[*flagSpec]bool, cur string) []candidate {
	var long, short []candidate
	for i := range flags {
		f := &flags[i]
		if used[f] {
			continue
		}
		long = append(long, candidate{"--" + f.long, f.desc})
		if f.short != "" {
			short = append(short, candidate{"-" + f.short, f.desc})
		}
	}
	if c := filterPrefix(long, cur); len(c) > 0 {
		return c
	}
	return filterPrefix(short, cur)
}

func filterPrefix(cands []candidate, prefix string) []candidate {
	var out []candidate
	for _, c := range cands {
		if strings.HasPrefix(c.value, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (a *App) values(k argKind, cur string) ([]candidate, bool) {
	var cands []candidate
	switch k {
	case argFile:
		return nil, true
	case argShell:
		for _, s := range []string{"bash", "zsh", "fish"} {
			cands = append(cands, candidate{value: s})
		}
	case argConfigured, argRunning, argKnown:
		cands = a.tunnelPorts(k)
	case argListening:
		cands = a.listeningPorts()
	case argUser:
		c := a.loadOptional()
		vals := []string{c.SSH.User}
		for _, t := range c.Tunnels {
			vals = append(vals, t.User)
		}
		cands = uniqueValues(vals)
	case argHost:
		c := a.loadOptional()
		vals := []string{c.SSH.Host}
		for _, t := range c.Tunnels {
			vals = append(vals, t.Host)
		}
		cands = uniqueValues(append(vals, sshConfigHosts()...))
	}
	return filterPrefix(cands, cur), false
}

// tunnelPorts lists configured and/or running tunnels, sorted by port.
func (a *App) tunnelPorts(k argKind) []candidate {
	c := a.loadOptional()
	running := map[int]state.State{}
	for _, s := range a.runningTunnels() {
		running[s.Port] = s
	}
	seen := map[int]bool{}
	var ps []int
	add := func(p int) {
		if !seen[p] {
			seen[p] = true
			ps = append(ps, p)
		}
	}
	if k != argRunning {
		for _, t := range c.Tunnels {
			if _, up := running[t.Port]; k == argKnown || !up {
				add(t.Port)
			}
		}
	}
	if k != argConfigured {
		for p := range running {
			add(p)
		}
	}
	sort.Ints(ps)

	var out []candidate
	for _, p := range ps {
		s, up := running[p]
		r := merged(c, p, s.Overrides)
		var parts []string
		if r.Name != "" {
			parts = append(parts, r.Name)
		}
		if r.User != "" && r.Host != "" {
			parts = append(parts, fmt.Sprintf("%s@%s:%d", r.User, r.Host, r.RemotePort))
		}
		desc := strings.Join(parts, " → ")
		if up && k == argKnown {
			desc = strings.TrimSpace(desc + " (running)")
		}
		out = append(out, candidate{strconv.Itoa(p), desc})
	}
	return out
}

// listeningPorts lists the ports with a listening process, one per port.
func (a *App) listeningPorts() []candidate {
	list := a.listPorts
	if list == nil {
		list = ports.List
	}
	ch := make(chan []ports.Listener, 1)
	go func() {
		ls, _ := list()
		ch <- ls
	}()
	var ls []ports.Listener
	select {
	case ls = <-ch:
	case <-time.After(listenerTimeout):
		return nil
	}
	var out []candidate
	seen := map[int]bool{}
	for _, l := range ls {
		if seen[l.Port] {
			continue
		}
		seen[l.Port] = true
		desc := l.Name
		if l.PID > 0 {
			desc = strings.TrimSpace(fmt.Sprintf("%s (pid %d)", l.Name, l.PID))
		}
		out = append(out, candidate{strconv.Itoa(l.Port), desc})
	}
	return out
}

// sshConfigHosts returns the concrete host aliases in ~/.ssh/config.
// Patterns and Include directives are ignored.
func sshConfigHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var hosts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		i := strings.IndexAny(line, " \t=")
		if i < 0 || !strings.EqualFold(line[:i], "host") {
			continue
		}
		for _, h := range strings.Fields(strings.TrimLeft(line[i:], " \t=")) {
			if !strings.ContainsAny(h, "*?!") {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// uniqueValues drops empty and repeated values, keeping order.
func uniqueValues(vals []string) []candidate {
	var out []candidate
	seen := map[string]bool{}
	for _, v := range vals {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, candidate{value: v})
		}
	}
	return out
}
