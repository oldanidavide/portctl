package process

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrStillRunning is returned by Terminate when the process survived SIGTERM
// and force was not requested.
var ErrStillRunning = errors.New("process is still running after SIGTERM (use --force to send SIGKILL)")

// RunDetaching runs ssh with arguments that include -f and waits for it to
// return. ssh reads passwords and host-key confirmations from the terminal while
// it is still in the foreground, then forks and the parent exits. Output of the
// backgrounded ssh keeps going to logFile.
func RunDetaching(args []string, logFile *os.File) error {
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return cmd.Run()
}

func CommandLine(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " ")), nil
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// StartTime returns the process start time as reported by ps. It is used as an
// opaque identity token to detect PID reuse.
func StartTime(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", errors.New("process not found")
	}
	return s, nil
}

// Matches reports whether pid runs ssh with exactly the expected arguments.
func Matches(pid int, expected []string) bool {
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		if err != nil || len(b) == 0 {
			return false
		}
		return argvMatches(strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), expected)
	}
	line, err := CommandLine(pid)
	return err == nil && lineMatches(line, expected)
}

// Find returns the PID of a process running ssh with exactly these arguments.
// Unlike a port lookup it needs neither lsof nor ss.
func Find(expected []string) (int, bool) {
	if runtime.GOOS == "linux" {
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			if pid, err := strconv.Atoi(e.Name()); err == nil && Matches(pid, expected) {
				return pid, true
			}
		}
		return 0, false
	}
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return 0, false
	}
	for _, l := range strings.Split(string(out), "\n") {
		pidStr, cmd, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(pidStr); err == nil && lineMatches(cmd, expected) {
			return pid, true
		}
	}
	return 0, false
}

func argvMatches(argv, expected []string) bool {
	return len(argv) == len(expected)+1 && filepath.Base(argv[0]) == "ssh" && slices.Equal(argv[1:], expected)
}

// lineMatches compares a command line as printed by ps, where the arguments
// are joined by spaces and their boundaries are lost.
func lineMatches(line string, expected []string) bool {
	prog, ok := strings.CutSuffix(strings.TrimSpace(line), " "+strings.Join(expected, " "))
	return ok && filepath.Base(prog) == "ssh"
}

// Alive reports whether a process with this PID exists and can be signalled.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func AliveOwned(pid int, expected []string) bool {
	return Alive(pid) && Matches(pid, expected)
}

// Stop terminates a tunnel process, escalating to SIGKILL after timeout.
func Stop(pid int, expected []string, timeout time.Duration) error {
	return Terminate(pid, func() bool { return AliveOwned(pid, expected) }, timeout, true)
}

// Terminate sends SIGTERM to pid and waits up to timeout for it to exit. owned
// is re-checked before every signal so a PID that was reused by an unrelated
// process is never signalled. With force, a survivor receives SIGKILL;
// otherwise ErrStillRunning is returned.
func Terminate(pid int, owned func() bool, timeout time.Duration, force bool) error {
	if pid <= 1 {
		return errors.New("refusing to signal PID " + strconv.Itoa(pid))
	}
	if !owned() {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !owned() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !owned() {
		return nil
	}
	if !force {
		return ErrStillRunning
	}
	if err := p.Signal(syscall.SIGKILL); err != nil {
		return err
	}
	for deadline = time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if !owned() {
			return nil
		}
	}
	return errors.New("process did not terminate")
}

func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if s, ok := ee.Sys().(syscall.WaitStatus); ok {
			return s.ExitStatus()
		}
	}
	return 1
}
