// Package workspaceroute carries only closed workspace directory/message
// streams. Direct-pair transport identity and original end-to-end identity are
// distinct. It never adds a discovered node to the direct peer registry.
package workspaceroute

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/workspace"
)

const (
	Version         = 1
	Path            = "/v1/mesh/workspace/route"
	Protocol        = "arm.mesh.workspace.route.v1"
	InnerProtocol   = "arm.workspace.message.v1"
	MaxControlBytes = 16 << 10
	MaxFrameBytes   = 64 << 10
	MaxPayloadBytes = MaxFrameBytes - 1
	MaxStreamBytes  = 1 << 20
	MaxLifetime     = 30 * time.Second
	MaxIdle         = 10 * time.Second
	MaxHops         = 8
	ClassMessage    = "message"
	ClassLookup     = "message_lookup"
	ClassDirectory  = "directory"
)

// Hop is the path index of the current destination. Pair/Revision belong ONLY
// to that immediate enrollment; they never attest the original source.
type Open struct {
	Version           int       `json:"schema_version"`
	Workspace         string    `json:"workspace"`
	Generation        string    `json:"generation"`
	Route             string    `json:"route_id"`
	Source            string    `json:"source_host"`
	Target            string    `json:"target_host"`
	SourceGeneration  int64     `json:"source_generation"`
	TargetGeneration  int64     `json:"target_generation"`
	DirectoryRevision int64     `json:"directory_revision"`
	Path              []string  `json:"path"`
	Hop               int       `json:"hop"`
	Class             string    `json:"class"`
	Expires           time.Time `json:"expires_at"`
	Pair              string    `json:"pair"`
	Revision          int       `json:"enrollment_revision"`
}

func validHex(v string, n int) bool {
	raw, e := hex.DecodeString(v)
	return e == nil && len(raw) == n && strings.ToLower(v) == v
}
func (o Open) Validate(now time.Time) error {
	if o.Version != Version || !validHex(o.Workspace, 16) || !validHex(o.Generation, 16) || !validHex(o.Route, 16) || !validHex(o.Source, 16) || !validHex(o.Target, 16) || o.Source == o.Target || o.SourceGeneration < 1 || o.TargetGeneration < 1 || o.DirectoryRevision < 1 || !validHex(o.Pair, 32) || o.Revision < 1 || !o.Expires.After(now) || o.Expires.After(now.Add(MaxLifetime+workspace.MaxClockSkew)) || o.Class != ClassMessage && o.Class != ClassDirectory && o.Class != ClassLookup {
		return errors.New("invalid closed routed workspace control")
	}
	if len(o.Path) < 2 || len(o.Path) > MaxHops || o.Hop < 1 || o.Hop >= len(o.Path) || o.Path[0] != o.Source || o.Path[len(o.Path)-1] != o.Target {
		return errors.New("invalid exact bounded route path or hop")
	}
	seen := map[string]bool{}
	for _, host := range o.Path {
		if !validHex(host, 16) || seen[host] {
			return errors.New("noncanonical or repeated route node")
		}
		seen[host] = true
	}
	return nil
}
func DecodeOpen(raw []byte, now time.Time) (Open, error) {
	var o Open
	if len(raw) == 0 || len(raw) > MaxControlBytes {
		return o, errors.New("route control exceeds bound")
	}
	if e := workspace.Decode(raw, &o); e != nil {
		return Open{}, e
	}
	if e := o.Validate(now); e != nil {
		return Open{}, e
	}
	return o, nil
}
func (o Open) Final() bool             { return o.Hop == len(o.Path)-1 }
func (o Open) ImmediateSource() string { return o.Path[o.Hop-1] }
func (o Open) ImmediateTarget() string { return o.Path[o.Hop] }

// Forward changes only next-hop admission fields. It cannot rewrite the origin,
// final destination, path, deadline, class or route/message namespace.
func (o Open) Forward(pair string, revision int) (Open, error) {
	if o.Final() {
		return Open{}, errors.New("final route cannot forward")
	}
	o.Hop++
	o.Pair = pair
	o.Revision = revision
	return o, o.Validate(time.Now())
}
func (o Open) Digest() string { raw, _ := json.Marshal(o); return digest(raw) }
