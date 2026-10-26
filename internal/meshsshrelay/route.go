// Package meshsshrelay carries only typed SSH/sync routes over pinned peers.
// Transport admission never grants execution or file access.
package meshsshrelay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/workspace"
)

const (
	Version                    = 1
	Path                       = "/v1/mesh/ssh/route"
	Protocol                   = "arm.mesh.ssh.route.v1"
	InnerProtocol              = "arm.workspace.ssh.v1"
	MaxControlBytes            = 16 << 10
	MaxFrameBytes              = 64 << 10
	MaxPayloadBytes            = MaxFrameBytes - 1
	MaxStreamBytes       int64 = 8 << 30
	MaxIdle                    = 30 * time.Second
	MaxNodes                   = 8
	MaxLifetime                = 15 * time.Minute
	ClassSSH                   = "ssh"
	ClassSSHIdentity           = "ssh_identity"
	ClassSync                  = "sync"
	ClassForward               = "forward"
	ForwardPath                = "/v1/mesh/forward/route"
	ForwardProtocol            = "arm.mesh.forward.route.v1"
	ForwardInnerProtocol       = "arm.workspace.forward.v1"
)

// Open is a hop assertion, never a credential. The immediate authenticated
// peer, current membership, explicit relay policy and final inner TLS identity
// must all be checked independently.
type Open struct {
	Workspace          string    `json:"workspace"`
	Generation         string    `json:"generation"`
	Route              string    `json:"route_id"`
	Source             string    `json:"source_host"`
	Target             string    `json:"target_host"`
	SourceGeneration   int64     `json:"source_generation"`
	TargetGeneration   int64     `json:"target_generation"`
	DirectoryRevision  int64     `json:"directory_revision"`
	DirectoryPin       string    `json:"directory_pin"`
	DirectorySPKI      string    `json:"directory_spki"`
	SourcePin          string    `json:"source_pin"`
	TargetPin          string    `json:"target_pin"`
	SSHHostKeyPin      string    `json:"ssh_host_key_pin"`
	Service            string    `json:"service,omitempty"`
	Path               []string  `json:"path"`
	Hop                int       `json:"hop"`
	Class              string    `json:"class"`
	Expires            time.Time `json:"expires_at"`
	Pair               string    `json:"pair"`
	EnrollmentRevision int       `json:"enrollment_revision"`
}

func hexBytes(s string, n int) bool {
	if len(s) != n*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	if err != nil {
		return false
	}
	for _, c := range s {
		if c >= 'A' && c <= 'F' {
			return false
		}
	}
	return true
}

func (o Open) Validate(now time.Time) error {
	if now.IsZero() || !hexBytes(o.Workspace, 16) || !hexBytes(o.Generation, 16) || !hexBytes(o.Route, 16) ||
		!hexBytes(o.Source, 16) || !hexBytes(o.Target, 16) || o.Source == o.Target ||
		!hexBytes(o.DirectoryPin, 32) || !hexBytes(o.DirectorySPKI, 32) ||
		!hexBytes(o.SourcePin, 32) || !hexBytes(o.TargetPin, 32) || !hexBytes(o.SSHHostKeyPin, 32) ||
		!hexBytes(o.Pair, 32) || o.SourceGeneration < 1 || o.TargetGeneration < 1 || o.DirectoryRevision < 1 ||
		o.EnrollmentRevision < 1 || o.Class != ClassSSH && o.Class != ClassSync && o.Class != ClassSSHIdentity && o.Class != ClassForward ||
		o.Class == ClassSSHIdentity && o.SSHHostKeyPin != strings.Repeat("0", 64) ||
		o.Class == ClassForward && (o.SSHHostKeyPin != strings.Repeat("0", 64) || !validService(o.Service)) ||
		o.Class != ClassForward && o.Service != "" ||
		o.Class != ClassSSHIdentity && o.Class != ClassForward && o.SSHHostKeyPin == strings.Repeat("0", 64) ||
		!o.Expires.After(now) || o.Expires.After(now.Add(MaxLifetime)) {
		return errors.New("invalid bounded SSH relay identity, class or expiry")
	}
	if len(o.Path) < 2 || len(o.Path) > MaxNodes || o.Hop < 1 || o.Hop >= len(o.Path) ||
		o.Path[0] != o.Source || o.Path[len(o.Path)-1] != o.Target {
		return errors.New("invalid SSH relay path or hop")
	}
	seen := make(map[string]bool, len(o.Path))
	for _, host := range o.Path {
		if !hexBytes(host, 16) || seen[host] {
			return errors.New("invalid or repeated SSH relay node")
		}
		seen[host] = true
	}
	return nil
}

func validService(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] == '-' {
		return false
	}
	for _, c := range name {
		if c != '-' && c != '_' && c != '.' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func DecodeOpen(raw []byte, now time.Time) (Open, error) {
	if len(raw) == 0 || len(raw) > MaxControlBytes {
		return Open{}, errors.New("SSH relay open exceeds control bound")
	}
	var o Open
	if err := workspace.Decode(raw, &o); err != nil {
		return Open{}, err
	}
	return o, o.Validate(now)
}

func (o Open) WireDigest() string {
	raw, _ := json.Marshal(o)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (o Open) Final() bool             { return o.Hop == len(o.Path)-1 }
func (o Open) ImmediateSource() string { return o.Path[o.Hop-1] }
func (o Open) ImmediateTarget() string { return o.Path[o.Hop] }

// BindingDigest belongs in the first authenticated inner control record. It
// excludes only the mutable immediate-hop enrollment fields. Both original
// endpoints compare this digest before destination admission or execution.
func (o Open) BindingDigest(now time.Time) (string, error) {
	if err := o.Validate(now); err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Workspace, Generation, Route, Source, Target                     string
		SourceGeneration, TargetGeneration, DirectoryRevision            int64
		DirectoryPin, DirectorySPKI, SourcePin, TargetPin, SSHHostKeyPin string
		Service                                                          string
		Path                                                             []string
		Class                                                            string
		Expires                                                          time.Time
	}{o.Workspace, o.Generation, o.Route, o.Source, o.Target,
		o.SourceGeneration, o.TargetGeneration, o.DirectoryRevision,
		o.DirectoryPin, o.DirectorySPKI, o.SourcePin, o.TargetPin, o.SSHHostKeyPin, o.Service, o.Path, o.Class, o.Expires})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// NextAuthority must be checked against the relay's current exact outbound
// direct pair before it opens the next hop. A graph hint cannot supply it.
type NextAuthority struct {
	Local, DirectPeer, Pair, Workspace, Generation string
	EnrollmentRevision                             int
	SSHRelayPermit                                 bool
	ValidUntil                                     time.Time
}

func (o Open) AuthorizeNext(now time.Time, a NextAuthority) error {
	if err := o.Validate(now); err != nil {
		return err
	}
	if o.Final() || a.Local != o.ImmediateTarget() || a.DirectPeer != o.Path[o.Hop+1] ||
		a.Workspace != o.Workspace || a.Generation != o.Generation ||
		!hexBytes(a.Pair, 32) || a.EnrollmentRevision < 1 || !a.SSHRelayPermit || !a.ValidUntil.After(now) {
		return errors.New("current exact outgoing SSH relay pair unavailable")
	}
	return nil
}

// Forward requires the current exact outgoing pair and changes only its hop
// enrollment fields. The original endpoints and route binding stay immutable.
func (o Open) Forward(now time.Time, a NextAuthority) (Open, error) {
	if err := o.AuthorizeNext(now, a); err != nil {
		return Open{}, err
	}
	o.Hop++
	o.Pair, o.EnrollmentRevision = a.Pair, a.EnrollmentRevision
	return o, o.Validate(now)
}

// HopAuthority is a current, local ledger observation. No field comes from a
// route header; callers must obtain it from pinned direct TLS and workspace
// membership state. SSHRelayPermit is a new, separately reviewed permission.
type HopAuthority struct {
	Local, AuthenticatedPeer, Pair                        string
	EnrollmentRevision                                    int
	Workspace, Generation                                 string
	Source, Target                                        string
	SourceGeneration, TargetGeneration, DirectoryRevision int64
	DirectoryPin, DirectorySPKI, SourcePin, TargetPin     string
	RelayEnabled, SSHRelayPermit                          bool
	ValidUntil                                            time.Time
}

func (o Open) AuthorizeHop(now time.Time, a HopAuthority) error {
	if err := o.Validate(now); err != nil {
		return err
	}
	if a.Local != o.ImmediateTarget() || a.AuthenticatedPeer != o.ImmediateSource() ||
		a.Pair != o.Pair || a.EnrollmentRevision != o.EnrollmentRevision ||
		a.Workspace != o.Workspace || a.Generation != o.Generation ||
		a.Source != o.Source || a.Target != o.Target ||
		a.SourceGeneration != o.SourceGeneration || a.TargetGeneration != o.TargetGeneration ||
		a.DirectoryRevision < o.DirectoryRevision || a.DirectoryPin != o.DirectoryPin || a.DirectorySPKI != o.DirectorySPKI ||
		a.SourcePin != o.SourcePin || a.TargetPin != o.TargetPin ||
		!a.SSHRelayPermit || !a.ValidUntil.After(now) ||
		!o.Final() && !a.RelayEnabled {
		return errors.New("current exact SSH relay hop authority unavailable")
	}
	return nil
}

// EndAuthority is established after pinned inner mutual TLS proves the
// ORIGINAL source, and after the destination checks its routed execution grant.
// It does not infer that grant from workspace membership or a relay's grant.
type EndAuthority struct {
	AuthenticatedSource, Local, DirectoryPin, DirectorySPKI, SourcePin, TargetPin, SSHHostKeyPin string
	Workspace, Generation                                                                        string
	SourceGeneration, TargetGeneration                                                           int64
	RoutedSSHExec, LeaseOwn, ServiceEnabled                                                      bool
	GrantValidUntil                                                                              time.Time
}

func (o Open) AuthorizeEnd(now time.Time, a EndAuthority) error {
	if err := o.Validate(now); err != nil {
		return err
	}
	if !o.Final() || a.AuthenticatedSource != o.Source || a.Local != o.Target ||
		a.DirectoryPin != o.DirectoryPin || a.DirectorySPKI != o.DirectorySPKI ||
		a.SourcePin != o.SourcePin || a.TargetPin != o.TargetPin || a.SSHHostKeyPin != o.SSHHostKeyPin ||
		a.Workspace != o.Workspace || a.Generation != o.Generation ||
		a.SourceGeneration != o.SourceGeneration || a.TargetGeneration != o.TargetGeneration ||
		!a.RoutedSSHExec || !a.LeaseOwn || !a.ServiceEnabled || !a.GrantValidUntil.After(now) {
		return errors.New("original source lacks current routed SSH destination authority")
	}
	return nil
}
