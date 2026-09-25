package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSaveLoadRemove(t *testing.T) {
	dir := t.TempDir()
	s := State{Port: 8000, PID: 42, StartedAt: time.Now().Round(0).UTC(), Command: []string{"-f", "-N"}, Overrides: Overrides{Host: "h"}}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v want %+v", got, s)
	}
	Remove(dir, 8000)
	if _, err := Load(dir, 8000); !os.IsNotExist(err) {
		t.Fatalf("expected not-exist after Remove, got %v", err)
	}
}

func TestListSortsAndSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []int{8000, 443, 3000} {
		if err := Save(dir, State{Port: p, PID: p}); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "9.json.tmp-1"), []byte(`{"port":9}`), 0600)
	var ports []int
	for _, s := range List(dir) {
		ports = append(ports, s.Port)
	}
	if !reflect.DeepEqual(ports, []int{443, 3000, 8000}) {
		t.Fatalf("List ports = %v", ports)
	}
}
