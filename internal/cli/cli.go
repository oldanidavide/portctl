package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/oldanidavide/portctl/internal/config"
	"github.com/oldanidavide/portctl/internal/ports"
	"github.com/oldanidavide/portctl/internal/ui"
)

// version is set at build time with -ldflags "-X github.com/oldanidavide/portctl/internal/cli.version=…".
var version = "dev"

type App struct {
	ConfigPath string
	StateDir   string
	Quiet      bool

	listPorts func() ([]ports.Listener, error) // ports.List when nil; replaced in tests
}

func New() (*App, error) {
	cp, err := config.ConfigPath()
	if err != nil {
		return nil, err
	}
	sd, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	return &App{ConfigPath: cp, StateDir: sd}, nil
}

// load reads the config file, which must exist.
func (a *App) load() (config.Config, error) {
	c, err := config.Load(a.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return c, a.errNoConfig()
	}
	return c, err
}

func (a *App) errNoConfig() error {
	return fmt.Errorf("configuration not found: %s\n  Run: %s", a.ConfigPath, cErr.Cmd("portctl config init"))
}

// loadIfExists reads the config file, or returns the defaults when there is none.
func (a *App) loadIfExists() (config.Config, bool, error) {
	c, err := config.Load(a.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return config.Default(), false, nil
	}
	return c, err == nil, err
}

// loadOptional returns the config when one exists; port commands work without
// it, so it stays silent about parse errors.
func (a *App) loadOptional() config.Config {
	c, err := config.Load(a.ConfigPath)
	if err != nil {
		return config.Default()
	}
	return c
}

var (
	cOut = ui.Out // styles for stdout
	cErr = ui.Err // styles for stderr
)

func out(q bool, f string, v ...any) {
	if !q {
		fmt.Printf(f+"\n", v...)
	}
}

// success prints a green ✓ line on stdout unless quiet.
func success(q bool, f string, v ...any) {
	if !q {
		fmt.Printf("%s %s\n", cOut.BoldGreen("✓"), cOut.Bold(fmt.Sprintf(f, v...)))
	}
}

// errf prints a red ✗ message on stderr; the first line is emphasized and the
// rest (details, hints) is printed as is.
func errf(f string, v ...any) {
	msg := fmt.Sprintf(f, v...)
	head, rest, _ := strings.Cut(msg, "\n")
	if rest != "" {
		rest = "\n" + rest
	}
	fmt.Fprintf(os.Stderr, "%s %s%s\n", cErr.BoldRed("✗"), cErr.Bold(head), rest)
}

// warnf prints a yellow ! message on stderr.
func warnf(f string, v ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", cErr.Yellow("!"), cErr.Yellow(fmt.Sprintf(f, v...)))
}

func fail(err error) int {
	errf("%v", err)
	return 1
}

// usageErr reports a bad invocation (exit code 2).
func usageErr(err error) int {
	errf("%v", err)
	return 2
}

func (a *App) Run(args []string) int {
	for len(args) > 0 {
		switch args[0] {
		case "-q", "--quiet":
			a.Quiet = true
		case "--no-color":
			ui.Disable()
			cOut, cErr = ui.Out, ui.Err
		default:
			goto dispatch
		}
		args = args[1:]
	}
dispatch:
	if len(args) == 0 {
		a.help()
		return 0
	}
	switch args[0] {
	case "tunnel":
		return a.tunnelCmd(args[1:])
	case "port":
		return a.portCmd(args[1:])
	case "config":
		return a.configCmd(args[1:])
	case "completion":
		return completion(args[1:])
	case "__complete": // hidden: called by the completion scripts
		return a.completeCmd(args[1:])
	case "help", "-h", "--help":
		a.help()
		return 0
	case "version", "--version":
		fmt.Println(cOut.Bold("portctl") + " " + cOut.Dim(version))
		return 0
	default:
		errf("unknown command: %s\n\nRun %s for usage.", args[0], cErr.Cmd("portctl help"))
		return 2
	}
}

func (a *App) help() {
	type row struct{ cmd, desc string }
	section := func(title string, rows []row) {
		fmt.Printf("\n%s\n", cOut.Bold(title))
		for _, r := range rows {
			fmt.Printf("  %s %s\n", cOut.Cyan(fmt.Sprintf("%-27s", r.cmd)), r.desc)
		}
	}
	fmt.Printf("%s — manage local ports and SSH port-forward tunnels\n\n%s\n  portctl <command> <subcommand> [options]\n",
		cOut.BoldGreen("portctl"), cOut.Bold("Usage:"))
	section("Tunnels:", []row{
		{"tunnel list [--json]", "List configured and running tunnels"},
		{"tunnel start <port>", "Start a tunnel in background (prompts for password if needed)"},
		{"tunnel stop <port>", "Stop a tunnel"},
		{"tunnel restart <port>", "Restart a tunnel"},
		{"tunnel start-all", "Start auto_start tunnels"},
		{"tunnel stop-all", "Stop all managed tunnels"},
		{"tunnel restart-all", "Restart running tunnels"},
		{"tunnel show <port>", "Show tunnel details"},
		{"tunnel logs <port>", "Show tunnel log"},
	})
	section("Ports:", []row{
		{"port list [--json]", "List listening TCP ports and their processes"},
		{"port info <port>", "Show which process is listening on a port"},
		{"port kill <port>", "Safely terminate the process listening on a port"},
		{"", cOut.Dim("(--yes skips confirmation, --force allows SIGKILL)")},
	})
	section("Other:", []row{
		{"config [path|init]", "Show or create the configuration"},
		{"completion <bash|zsh|fish>", "Print shell completion"},
		{"version", "Print version"},
	})
	section("Global options:", []row{
		{"-q, --quiet", "Suppress success messages"},
		{"--no-color", "Disable colors (also: NO_COLOR=1)"},
	})
	fmt.Printf("\nRun %s for start options.\n", cOut.Cmd("portctl tunnel start --help"))
}

// newFlags returns a flag set for a subcommand; usage is the argument
// synopsis shown by --help, e.g. "<port> [options]".
func newFlags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: portctl %s %s\n", name, usage)
		fs.PrintDefaults()
	}
	return fs
}

// parseArgs parses flags placed before, between or after the positional
// arguments and returns the latter. When ok is false the problem has been
// reported and rc is the exit code (0 for --help).
func parseArgs(fs *flag.FlagSet, args []string) (pos []string, rc int, ok bool) {
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, 0, false
			}
			return nil, 2, false
		}
		if fs.NArg() == 0 {
			return pos, 0, true
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// parseNoArgs is parseArgs for commands that take only flags.
func parseNoArgs(fs *flag.FlagSet, args []string) (int, bool) {
	pos, rc, ok := parseArgs(fs, args)
	if !ok {
		return rc, false
	}
	if len(pos) > 0 {
		return usageErr(fmt.Errorf("unexpected argument: %s", pos[0])), false
	}
	return 0, true
}

// parsePortArgs is parseArgs for commands that take exactly one <port>.
func parsePortArgs(fs *flag.FlagSet, args []string) (port, rc int, ok bool) {
	pos, rc, ok := parseArgs(fs, args)
	if !ok {
		return 0, rc, false
	}
	switch {
	case len(pos) == 0:
		return 0, usageErr(errors.New("missing port")), false
	case len(pos) > 1:
		return 0, usageErr(fmt.Errorf("unexpected argument: %s", pos[1])), false
	}
	p, err := strconv.Atoi(pos[0])
	if err != nil || p < 1 || p > 65535 {
		return 0, usageErr(fmt.Errorf("invalid port: %s", pos[0])), false
	}
	return p, 0, true
}

func (a *App) configCmd(args []string) int {
	if len(args) == 0 {
		fmt.Printf("%s %s\n", cOut.Label("Config:"), a.ConfigPath)
		return 0
	}
	switch args[0] {
	case "path":
		fmt.Println(a.ConfigPath)
		return 0
	case "init":
		cp, err := config.ConfigPath()
		if err != nil {
			return fail(err)
		}
		if e := config.Init(cp); e != nil {
			return fail(e)
		}
		success(false, "Created configuration")
		fmt.Printf("  %s\n", cp)
		return 0
	default:
		errf("unknown config command: %s", args[0])
		return 2
	}
}

// The completion scripts delegate to "portctl __complete" (see complete.go),
// so they stay the same when commands or flags change.
const bashCompletion = `# bash completion for portctl (works with bash 3.2 and later)
_portctl() {
  local cur=${COMP_WORDS[COMP_CWORD]} line f
  COMPREPLY=()
  while IFS= read -r line; do
    if [ "$line" = ":file" ]; then
      compopt -o filenames 2>/dev/null
      while IFS= read -r f; do COMPREPLY+=("$f"); done < <(compgen -f -- "$cur")
    elif [ -n "$line" ]; then
      COMPREPLY+=("${line%%$'\t'*}")
    fi
  done < <(command portctl __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null)
}
complete -F _portctl portctl
`

const zshCompletion = `#compdef portctl
# zsh completion for portctl

_portctl() {
  local -a cands
  local line value desc files=0
  for line in "${(@f)$(command portctl __complete "${(@)words[2,CURRENT]}" 2>/dev/null)}"; do
    if [[ $line == :file ]]; then
      files=1
    elif [[ -n $line ]]; then
      value=${line%%$'\t'*}
      value=${value//:/\\:}
      if [[ $line == *$'\t'* ]]; then
        desc=${line#*$'\t'}
        cands+=("$value:$desc")
      else
        cands+=("$value")
      fi
    fi
  done
  (( files )) && _files
  (( $#cands )) && _describe -t values portctl cands
  return 0
}

if [[ $funcstack[1] == _portctl ]]; then
  _portctl "$@"
else
  # sourced from .zshrc: enable the completion system if nobody did yet
  (( $+functions[compdef] )) || { autoload -Uz compinit && compinit -i; }
  compdef _portctl portctl
fi
`

const fishCompletion = `# fish completion for portctl
function __portctl_complete
    # -x is fish 4; older versions only have -o
    set -l words (commandline -xpc 2>/dev/null; or commandline -opc)
    set -e words[1]
    set -l cur (commandline -ct)
    for line in (command portctl __complete $words "$cur" 2>/dev/null)
        if test "$line" = ":file"
            __fish_complete_path "$cur"
        else if test -n "$line"
            echo $line
        end
    end
end
complete -c portctl -e
complete -c portctl -f -a '(__portctl_complete)'
`

func completion(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: portctl completion bash|zsh|fish")
		return 2
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		errf("unsupported shell: %s", args[0])
		return 2
	}
	return 0
}
