package ports

import (
	"strings"
	"testing"
)

func TestParseLsof(t *testing.T) {
	in := `p621
cControlCenter
u501
Ldavide
f8
tIPv4
n*:7000
f9
tIPv6
n*:7000
f10
tIPv4
n*:7000
p4123
cnode
u0
Lroot
f23
tIPv4
n127.0.0.1:8080
f24
tIPv6
n[::1]:8080
`
	got := parseLsof(strings.NewReader(in))
	if len(got) != 4 {
		t.Fatalf("got %d listeners: %#v", len(got), got)
	}
	want := Listener{Port: 8080, Addr: "[::1]", Family: "IPv6", PID: 4123, UID: 0, User: "root", Name: "node"}
	if got[3] != want {
		t.Fatalf("got %#v want %#v", got[3], want)
	}
	if got[0].Name != "ControlCenter" || got[0].UID != 501 || got[0].Addr != "*" {
		t.Fatalf("unexpected first listener %#v", got[0])
	}
	if g := Owners(got); len(g) != 2 || Addrs(g[1]) != "127.0.0.1, [::1]" {
		t.Fatalf("unexpected grouping %#v", g)
	}
}

func TestParseSS(t *testing.T) {
	in := `LISTEN 0      4096       127.0.0.1:8080      0.0.0.0:*    users:(("python3",pid=1234,fd=3))
LISTEN 0      128           [::]:22           [::]:*
LISTEN 0      511              *:3000            *:*    users:(("node",pid=77,fd=20),("node",pid=77,fd=21))
`
	got := parseSS(strings.NewReader(in))
	if len(got) != 3 {
		t.Fatalf("got %#v", got)
	}
	if got[0].PID != 1234 || got[0].Name != "python3" || got[0].Port != 8080 || got[0].Addr != "127.0.0.1" {
		t.Errorf("bad first: %#v", got[0])
	}
	if got[1].PID != 0 || got[1].Port != 22 || got[1].Family != "IPv6" {
		t.Errorf("bad hidden owner: %#v", got[1])
	}
	if got[2].PID != 77 || got[2].Port != 3000 {
		t.Errorf("bad dedup: %#v", got[2])
	}
}

func TestSplitAddr(t *testing.T) {
	for _, c := range []struct {
		in   string
		addr string
		port int
		ok   bool
	}{
		{"127.0.0.1:8080", "127.0.0.1", 8080, true},
		{"[::1]:443", "[::1]", 443, true},
		{"*:22", "*", 22, true},
		{"*:*", "", 0, false},
		{"garbage", "", 0, false},
	} {
		a, p, ok := splitAddr(c.in)
		if a != c.addr || p != c.port || ok != c.ok {
			t.Errorf("splitAddr(%q) = %q,%d,%v", c.in, a, p, ok)
		}
	}
}
