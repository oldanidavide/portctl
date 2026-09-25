// Package ports inspects TCP sockets in LISTEN state and the processes that
// own them. It shells out to lsof (macOS, Linux) or ss (Linux fallback)
// without ever going through a shell.
package ports

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Listener is one process listening on one local address.
type Listener struct {
	Port    int
	Addr    string
	Family  string
	PID     int
	UID     int
	User    string
	Name    string
	Command string
}

// ErrNoTool means neither lsof nor ss is available.
var ErrNoTool = errors.New("cannot inspect ports: install lsof (or iproute2's ss on Linux)")

// List returns every TCP listener visible to the current user, sorted by port.
func List() ([]Listener, error) { return query(0) }

// ByPort returns the listeners on a single TCP port.
func ByPort(port int) ([]Listener, error) { return query(port) }

func query(port int) ([]Listener, error) {
	var ls []Listener
	var err error
	if path, e := exec.LookPath("lsof"); e == nil {
		ls, err = viaLsof(path, port)
	} else if path, e := exec.LookPath("ss"); e == nil && runtime.GOOS == "linux" {
		ls, err = viaSS(path, port)
	} else {
		return nil, ErrNoTool
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ls, func(i, j int) bool {
		if ls[i].Port != ls[j].Port {
			return ls[i].Port < ls[j].Port
		}
		return ls[i].PID < ls[j].PID
	})
	return ls, nil
}

func viaLsof(path string, port int) ([]Listener, error) {
	spec := "-iTCP"
	if port > 0 {
		spec += ":" + strconv.Itoa(port)
	}
	out, err := exec.Command(path, "-nP", spec, "-sTCP:LISTEN", "-FpcuLtn").Output()
	if err != nil && len(out) == 0 {
		// lsof exits 1 when nothing matches; that is an empty result.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	ls := parseLsof(bytes.NewReader(out))
	if port > 0 {
		ls = filterPort(ls, port)
	}
	return ls, nil
}

// parseLsof parses `lsof -F pcuLtn` output: one field per line, the first
// character identifies the field. p/c/u/L describe a process; t/n describe
// one of its file descriptors.
func parseLsof(r io.Reader) []Listener {
	var out []Listener
	seen := map[string]bool{}
	var cur Listener
	var family string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
			pid, _ := strconv.Atoi(v)
			cur = Listener{PID: pid, UID: -1}
			family = ""
		case 'c':
			cur.Name = v
		case 'u':
			if uid, err := strconv.Atoi(v); err == nil {
				cur.UID = uid
			}
		case 'L':
			cur.User = v
		case 'f':
			family = ""
		case 't':
			family = v
		case 'n':
			addr, port, ok := splitAddr(v)
			if !ok {
				continue
			}
			l := cur
			l.Addr, l.Port, l.Family = addr, port, family
			key := strconv.Itoa(l.PID) + "|" + l.Family + "|" + l.Addr + "|" + strconv.Itoa(l.Port)
			if !seen[key] {
				seen[key] = true
				out = append(out, l)
			}
		}
	}
	return out
}

var ssUsers = regexp.MustCompile(`\("([^"]*)",pid=(\d+)`)

func viaSS(path string, port int) ([]Listener, error) {
	out, err := exec.Command(path, "-ltnpH").Output()
	if err != nil {
		return nil, err
	}
	ls := parseSS(bytes.NewReader(out))
	for i := range ls {
		ls[i].UID, ls[i].User = procOwner(ls[i].PID)
	}
	if port > 0 {
		ls = filterPort(ls, port)
	}
	return ls, nil
}

// parseSS parses `ss -ltnpH` lines such as:
//
//	LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:(("python3",pid=1234,fd=3))
func parseSS(r io.Reader) []Listener {
	var out []Listener
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		addr, port, ok := splitAddr(f[3])
		if !ok {
			continue
		}
		family := "IPv4"
		if strings.HasPrefix(addr, "[") {
			family = "IPv6"
		}
		rest := strings.Join(f[5:], " ")
		matches := ssUsers.FindAllStringSubmatch(rest, -1)
		if len(matches) == 0 {
			// Owned by another user: ss hides the process without root.
			out = append(out, Listener{Port: port, Addr: addr, Family: family, UID: -1})
			continue
		}
		seen := map[int]bool{}
		for _, m := range matches {
			pid, _ := strconv.Atoi(m[2])
			if seen[pid] {
				continue
			}
			seen[pid] = true
			out = append(out, Listener{Port: port, Addr: addr, Family: family, PID: pid, Name: m[1], UID: -1})
		}
	}
	return out
}

func procOwner(pid int) (int, string) {
	st, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return -1, ""
	}
	uid := statUID(st)
	if uid < 0 {
		return -1, ""
	}
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return uid, u.Username
	}
	return uid, strconv.Itoa(uid)
}

// splitAddr splits "127.0.0.1:8080", "*:8080" or "[::1]:8080".
func splitAddr(s string) (string, int, bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", 0, false
	}
	p, err := strconv.Atoi(s[i+1:])
	if err != nil || p < 1 || p > 65535 {
		return "", 0, false
	}
	return s[:i], p, true
}

func filterPort(ls []Listener, port int) []Listener {
	out := ls[:0]
	for _, l := range ls {
		if l.Port == port {
			out = append(out, l)
		}
	}
	return out
}

// Owners groups listeners by PID, preserving order. Listeners without a
// visible PID are grouped under PID 0.
func Owners(ls []Listener) [][]Listener {
	idx := map[int]int{}
	var out [][]Listener
	for _, l := range ls {
		i, ok := idx[l.PID]
		if !ok {
			i = len(out)
			idx[l.PID] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], l)
	}
	return out
}

// Addrs renders the distinct addresses of a listener group, e.g. "127.0.0.1, [::1]".
func Addrs(ls []Listener) string {
	var parts []string
	seen := map[string]bool{}
	for _, l := range ls {
		if !seen[l.Addr] {
			seen[l.Addr] = true
			parts = append(parts, l.Addr)
		}
	}
	return strings.Join(parts, ", ")
}

func statUID(st os.FileInfo) int {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid)
	}
	return -1
}
