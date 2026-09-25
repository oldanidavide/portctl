package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type State struct {
	Port      int       `json:"port"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Command   []string  `json:"command"`
	Overrides Overrides `json:"overrides"`
}

// Overrides are the --user/--host/--identity values a tunnel was started
// with; restart reuses them.
type Overrides struct {
	User     string `json:"user,omitempty"`
	Host     string `json:"host,omitempty"`
	Identity string `json:"identity,omitempty"`
}

func Paths(dir string, port int) (jsonPath, logPath string) {
	return filepath.Join(dir, fmt.Sprintf("%d.json", port)), filepath.Join(dir, "logs", fmt.Sprintf("%d.log", port))
}

func Save(dir string, s State) error {
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0700); err != nil {
		return err
	}
	jp, _ := Paths(dir, s.Port)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := jp + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, jp)
}

func Load(dir string, port int) (State, error) {
	jp, _ := Paths(dir, port)
	b, err := os.ReadFile(jp)
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

func Remove(dir string, port int) { jp, _ := Paths(dir, port); _ = os.Remove(jp) }

// List returns every readable tunnel state file in dir, sorted by port.
func List(dir string) []State {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []State
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var s State
		if json.Unmarshal(b, &s) == nil && s.Port > 0 {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
