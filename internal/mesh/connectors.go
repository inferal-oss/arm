package mesh

import (
	"fmt"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Connector pins operator-selected installed helper paths, not peer trust.
// Only native reviewed operations may write this local execution configuration.
type Connector struct {
	ID          string         `json:"id"`
	Destination string         `json:"ssh_destination"`
	Binary      string         `json:"remote_binary"`
	Home        string         `json:"remote_home,omitempty"`
	Reason      string         `json:"reason"`
	Actor       local.Identity `json:"actor"`
	At          time.Time      `json:"reviewed_at"`
}

func (c Connector) Validate() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(c.ID) || c.Destination == "" || len(c.Destination) > 512 || c.Destination[0] == '-' || !regexp.MustCompile(`^(arm|/[A-Za-z0-9_./-]+)$`).MatchString(c.Binary) || c.Home != "" && !filepath.IsAbs(c.Home) || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 4096 {
		return model.Err("invalid_request", "connector needs ID, direct SSH destination, safe installed binary, optional absolute home and reason")
	}
	for _, r := range c.Destination {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return model.Err("invalid_request", "SSH destination must be one bounded argument")
		}
	}
	return nil
}
func ReadConnectors(home string) ([]Connector, error) {
	raw, e := privateFile(filepath.Join(home, "mesh-connectors.json"))
	if os.IsNotExist(e) {
		return []Connector{}, nil
	}
	if e != nil {
		return nil, e
	}
	var out []Connector
	if e = Decode(raw, &out); e != nil {
		return nil, e
	}
	if len(out) > 32 {
		return nil, fmt.Errorf("connector catalogue exceeds bound")
	}
	seen := map[string]bool{}
	for _, c := range out {
		if e = c.Validate(); e != nil {
			return nil, e
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("duplicate connector ID")
		}
		seen[c.ID] = true
	}
	return out, nil
}
func SaveConnector(home string, c Connector) error {
	if e := c.Validate(); e != nil {
		return e
	}
	if e := local.Prepare(home); e != nil {
		return e
	}
	unlock, e := LockLifecycle(home)
	if e != nil {
		return e
	}
	defer unlock()
	all, e := ReadConnectors(home)
	if e != nil {
		return e
	}
	for _, old := range all {
		if old.ID == c.ID {
			return model.Err("already_exists", "connector IDs are immutable; choose a new reviewed ID")
		}
	}
	if len(all) >= 32 {
		return model.Err("history_full", "connector catalogue is full; no reviewed record removed")
	}
	c.Actor = local.CurrentIdentity()
	c.At = time.Now().UTC()
	return save(filepath.Join(home, "mesh-connectors.json"), append(all, c))
}
