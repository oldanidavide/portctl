package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oldanidavide/portctl/internal/ports"
	"github.com/oldanidavide/portctl/internal/state"
)

const completeConfig = `ssh:
  user: davide
  host: production

tunnels:
  - port: 8000
    name: development
  - port: 3306
    name: mysql
    user: root
    host: database-server
`

// completeApp returns an App with the config above, a running tunnel on
// port 8000 and two fake listening ports.
func completeApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cp := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cp, []byte(completeConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: cp, StateDir: filepath.Join(dir, "state")}
	a.listPorts = func() ([]ports.Listener, error) {
		return []ports.Listener{
			{Port: 5432, PID: 10, Name: "postgres"},
			{Port: 5432, PID: 10, Name: "postgres", Addr: "[::1]"},
			{Port: 8000, PID: 11, Name: "ssh"},
		}, nil
	}

	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available")
	}
	link := filepath.Join(dir, "ssh")
	if err := os.Symlink(sleep, link); err != nil {
		t.Skip(err)
	}
	// sleep sums its arguments, so a unique fraction makes the command line unique.
	args := []string{"30", fmt.Sprintf("0.%09d", time.Now().Nanosecond())}
	cmd := exec.Command(link, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	time.Sleep(100 * time.Millisecond)
	if err := state.Save(a.StateDir, state.State{Port: 8000, PID: cmd.Process.Pid, Command: args}); err != nil {
		t.Fatal(err)
	}
	return a
}

func values(cands []candidate) []string {
	out := []string{}
	for _, c := range cands {
		out = append(out, c.value)
	}
	return out
}

func TestComplete(t *testing.T) {
	a := completeApp(t)
	for _, c := range []struct {
		line  string // words separated by spaces; a trailing space means an empty current word
		want  []string
		files bool
	}{
		{"", []string{"tunnel", "port", "config", "completion", "help", "version"}, false},
		{"t", []string{"tunnel"}, false},
		{"p", []string{"port"}, false},
		{"-", []string{"--quiet", "--no-color"}, false},
		{"-q --no-color tunnel st", []string{"start", "stop", "start-all", "stop-all"}, false},
		{"tunnel start ", []string{"3306"}, false}, // 8000 is already running
		{"tunnel stop ", []string{"8000"}, false},
		{"tunnel show ", []string{"3306", "8000"}, false},
		{"tunnel start 3306 ", []string{"--user", "--host", "--identity", "--foreground", "--dry-run"}, false},
		{"tunnel start 3306 --dry-run -", []string{"--user", "--host", "--identity", "--foreground"}, false},
		{"tunnel start -u x --d", []string{"--dry-run"}, false},
		{"tunnel start --host=h --h", []string{}, false},
		{"tunnel start -H", []string{"-H"}, false},
		{"tunnel start -H ", []string{"production", "database-server"}, false},
		{"tunnel start --user ", []string{"davide", "root"}, false},
		{"tunnel start --user = r", []string{"root"}, false},
		{"tunnel start --identity ", []string{}, true},
		{"tunnel stop 8000 ", []string{}, false},
		{"tunnel ls ", []string{"--json"}, false},
		{"port kill ", []string{"5432", "8000"}, false},
		{"port kill 5", []string{"5432"}, false},
		{"port kill 5432 -", []string{"--yes", "--force"}, false},
		{"port kill -", []string{"--yes", "--force"}, false},
		{"port kill -y", []string{"-y"}, false},
		{"config ", []string{"path", "init"}, false},
		{"completion z", []string{"zsh"}, false},
		{"nope ", []string{}, false},
		{"version ", []string{}, false},
	} {
		got, files := a.complete(strings.Split(c.line, " "))
		if !reflect.DeepEqual(values(got), c.want) || files != c.files {
			t.Errorf("%q: got %q files=%v, want %q files=%v", c.line, values(got), files, c.want, c.files)
		}
	}
}

func TestCompleteDescriptions(t *testing.T) {
	a := completeApp(t)
	got, _ := a.complete([]string{"tunnel", "show", ""})
	want := []candidate{
		{"3306", "mysql → root@database-server:3306"},
		{"8000", "development → davide@production:8000 (running)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	got, _ = a.complete([]string{"port", "info", ""})
	if len(got) == 0 || got[0] != (candidate{"5432", "postgres (pid 10)"}) {
		t.Errorf("got %q", got)
	}
}

func TestCompleteWithoutConfig(t *testing.T) {
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"), StateDir: t.TempDir()}
	a.listPorts = func() ([]ports.Listener, error) { return nil, ports.ErrNoTool }
	for _, line := range [][]string{{"tunnel", "start", ""}, {"tunnel", "start", "-u", ""}, {"port", "kill", ""}} {
		if got, _ := a.complete(line); len(got) != 0 {
			t.Errorf("%q: got %q", line, got)
		}
	}
}

func TestSSHConfigHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "Host prod staging\n  User x\nHost *.internal !bad\nhost=db\n  Hostname db.example\nHOST\tweb\n# Host commented\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := sshConfigHosts(), []string{"prod", "staging", "db", "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCompletionScriptsSyntax checks that each script parses in its shell,
// when that shell is installed.
func TestCompletionScriptsSyntax(t *testing.T) {
	for shell, script := range map[string]string{"bash": bashCompletion, "zsh": zshCompletion, "fish": fishCompletion} {
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not installed, skipped", shell)
			continue
		}
		flag := "-n"
		if shell == "fish" {
			flag = "--no-execute"
		}
		cmd := exec.Command(path, flag)
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", shell, err, out)
		}
	}
}

// fakePortctl puts a portctl on PATH that answers __complete with fixed
// candidates and records the arguments it received.
func fakePortctl(t *testing.T) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s|' \"$@\" > " + argsFile + "\n" +
		"case \"$4\" in --identity) echo :file ;; *) printf '8000\\tdev → a@b\\n3306\\n' ;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "portctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func TestBashCompletionScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	argsFile := fakePortctl(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	test := bashCompletion + `
t() { COMP_WORDS=("$@"); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); _portctl; echo "${COMPREPLY[*]}"; }
t portctl tunnel start ""
cd ` + dir + ` && t portctl tunnel start --identity k
`
	out, err := exec.Command(bash, "-c", test).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := string(out), "8000 3306\nkey.pem\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(argsFile); string(b) != "__complete|tunnel|start|--identity|k|" {
		t.Errorf("portctl called with %q", b)
	}
}

func TestZshCompletionScript(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	argsFile := fakePortctl(t)
	test := `compdef() { print "compdef $*"; }
_describe() { print -l -- "${(@P)${@[-1]}}"; }
_files() { print FILES; }
` + zshCompletion + `
words=(portctl tunnel start ""); CURRENT=4; _portctl
words=(portctl tunnel start --identity ""); CURRENT=5; _portctl
`
	out, err := exec.Command(zsh, "-f", "-c", test).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := string(out), "compdef _portctl portctl\n8000:dev → a@b\n3306\nFILES\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(argsFile); string(b) != "__complete|tunnel|start|--identity||" {
		t.Errorf("portctl called with %q", b)
	}
}

func TestFishCompletionScript(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not installed")
	}
	fakePortctl(t)
	test := fishCompletion + `
complete -C "portctl tunnel start "
`
	out, err := exec.Command(fish, "--no-config", "-c", test).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := string(out), "3306\n8000\tdev → a@b\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
