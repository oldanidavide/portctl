package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestArgvMatches(t *testing.T) {
	want := []string{"-f", "-N", "host"}
	for _, c := range []struct {
		argv []string
		ok   bool
	}{
		{[]string{"ssh", "-f", "-N", "host"}, true},
		{[]string{"/usr/bin/ssh", "-f", "-N", "host"}, true},
		{[]string{"sshd", "-f", "-N", "host"}, false},
		{[]string{"ssh", "-f", "-N", "host", "extra"}, false},
		{[]string{"ssh", "-N", "-f", "host"}, false},
	} {
		if got := argvMatches(c.argv, want); got != c.ok {
			t.Errorf("argvMatches(%q) = %v", c.argv, got)
		}
	}
}

func TestLineMatches(t *testing.T) {
	want := []string{"-L", "127.0.0.1:80:127.0.0.1:80", "host"}
	for _, c := range []struct {
		line string
		ok   bool
	}{
		{"ssh -L 127.0.0.1:80:127.0.0.1:80 host", true},
		{"/usr/bin/ssh -L 127.0.0.1:80:127.0.0.1:80 host\n", true},
		{"ssh -L 127.0.0.1:8000:127.0.0.1:8000 host", false},
		{"ssh -L 127.0.0.1:80:127.0.0.1:80 otherhost", false},
		{"vim ssh -L 127.0.0.1:80:127.0.0.1:80 host", false},
	} {
		if got := lineMatches(c.line, want); got != c.ok {
			t.Errorf("lineMatches(%q) = %v", c.line, got)
		}
	}
}

// fakeSSH starts a long-running process whose argv[0] is named "ssh".
func fakeSSH(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not available")
	}
	link := filepath.Join(t.TempDir(), "ssh")
	if err := os.Symlink(sleep, link); err != nil {
		t.Skip(err)
	}
	cmd := exec.Command(link, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	time.Sleep(100 * time.Millisecond)
	return cmd
}

func TestFindMatchesAndTerminate(t *testing.T) {
	// sleep sums its arguments, so a unique fraction makes the command line unique.
	args := []string{"30", fmt.Sprintf("0.%09d", time.Now().Nanosecond())}
	cmd := fakeSSH(t, args...)
	pid := cmd.Process.Pid

	if !Matches(pid, args) {
		t.Fatal("Matches should recognise the fake ssh process")
	}
	if Matches(pid, []string{"31", args[1]}) {
		t.Fatal("Matches must compare arguments exactly")
	}
	if got, ok := Find(args); !ok || got != pid {
		t.Fatalf("Find = %d, %v; want %d", got, ok, pid)
	}
	if _, err := StartTime(pid); err != nil {
		t.Fatalf("StartTime: %v", err)
	}

	// Reap the child as soon as it exits so it does not linger as a zombie.
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	exited := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	if err := Terminate(pid, func() bool { return !exited() }, 2*time.Second, false); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if !exited() {
		t.Fatal("process still running after Terminate")
	}
}

func TestTerminateRefusesInit(t *testing.T) {
	if err := Terminate(1, func() bool { return true }, time.Millisecond, true); err == nil {
		t.Fatal("expected refusal for PID 1")
	}
}
