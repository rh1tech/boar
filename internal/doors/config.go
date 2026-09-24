// Package doors runs external "door" programs for callers, the classic BBS
// way: the BBS writes drop files describing the caller (DOOR.SYS and
// DORINFO1.DEF), starts the program, and wires the caller's terminal to it.
package doors

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// IO modes: how a door talks to the caller.
const (
	IOStdio = "stdio" // the program reads stdin and writes stdout (native doors)
	IOTCP   = "tcp"   // the program connects to 127.0.0.1:{port} (DOSBox serial)
)

const defaultMaxMinutes = 60

// Door is one entry in the doors config file.
type Door struct {
	Key         string   `json:"key"`         // short id, e.g. "lord"
	Name        string   `json:"name"`        // shown in the menu
	Description string   `json:"description"` // one line under the name
	Command     []string `json:"command"`     // program and arguments; {vars} are filled in
	Dir         string   `json:"dir"`         // working directory; "" = BBS working directory
	IO          string   `json:"io"`          // "stdio" (default) or "tcp"
	SysopOnly   bool     `json:"sysop_only"`  // hidden from other callers
	SingleNode  bool     `json:"single_node"` // one caller at a time
	MaxMinutes  int      `json:"max_minutes"` // time limit per visit; 0 = 60
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,15}$`)

// LoadConfig reads the doors file. A missing file means no doors.
func LoadConfig(path string) ([]Door, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("doors: %w", err)
	}
	var doors []Door
	if err := json.Unmarshal(data, &doors); err != nil {
		return nil, fmt.Errorf("doors: parse %s: %w", path, err)
	}
	return Validate(doors)
}

// Validate fills in defaults and rejects broken entries.
func Validate(doors []Door) ([]Door, error) {
	seen := map[string]bool{}
	out := make([]Door, 0, len(doors))
	for i, d := range doors {
		switch {
		case !keyPattern.MatchString(d.Key):
			return nil, fmt.Errorf("doors: entry %d: key %q must be 1-16 of a-z 0-9 _ -", i+1, d.Key)
		case seen[d.Key]:
			return nil, fmt.Errorf("doors: duplicate key %q", d.Key)
		case d.Name == "":
			return nil, fmt.Errorf("doors: %s: name is required", d.Key)
		case len(d.Command) == 0 || d.Command[0] == "":
			return nil, fmt.Errorf("doors: %s: command is required", d.Key)
		}
		if d.IO == "" {
			d.IO = IOStdio
		}
		if d.IO != IOStdio && d.IO != IOTCP {
			return nil, fmt.Errorf("doors: %s: io must be %q or %q", d.Key, IOStdio, IOTCP)
		}
		if d.MaxMinutes <= 0 {
			d.MaxMinutes = defaultMaxMinutes
		}
		seen[d.Key] = true
		out = append(out, d)
	}
	return out, nil
}
