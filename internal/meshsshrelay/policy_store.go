package meshsshrelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/workspace"
	"golang.org/x/sys/unix"
)

const policyName = "ssh-relay-policy.json"
const policyLockName = "ssh-relay-policy.lock"
const maxPolicyBytes = 256 << 10

// Policy is the local operator's durable, separately reviewed SSH routing
// authority. An absent file means no relay or routed execution grants.
type Policy struct {
	Schema         int                 `json:"schema"`
	Local          string              `json:"local"`
	LocalPin       string              `json:"local_pin"`
	Revision       int64               `json:"revision"`
	Relays         []RelayGrant        `json:"relays"`
	Destinations   []DestinationGrant  `json:"destinations"`
	Forwards       []meshforward.Grant `json:"forwards,omitempty"`
	CreditLedgerID string              `json:"forward_credit_ledger_id,omitempty"`
	Events         []PolicyEvent       `json:"events"`
}

type PolicyEvent struct {
	Revision     int64     `json:"revision"`
	Action       string    `json:"action"`
	Reason       string    `json:"reason"`
	BeforeSHA256 string    `json:"before_sha256"`
	AfterSHA256  string    `json:"after_sha256"`
	At           time.Time `json:"at"`
}

func (p Policy) Validate(local, localPin string) error {
	if p.Schema != 1 || !hexBytes(local, 16) || !hexBytes(localPin, 32) || p.Local != local || p.LocalPin != localPin ||
		p.Revision < 1 || len(p.Relays) > 64 || len(p.Destinations) > 128 || len(p.Forwards) > 128 || len(p.Events) > 256 ||
		p.CreditLedgerID != "" && !hexBytes(p.CreditLedgerID, 16) {
		return errors.New("invalid local routed SSH policy binding or capacity")
	}
	seen := map[string]bool{}
	for _, g := range p.Relays {
		if (g.Validate() != nil && !g.legacyUnbudgeted()) || g.Local != local {
			return errors.New("invalid SSH relay opt-in")
		}
		key := g.Workspace + ":" + g.Generation
		if seen[key] {
			return errors.New("duplicate SSH relay opt-in")
		}
		seen[key] = true
	}
	clear(seen)
	creditNonces := map[string]bool{}
	for _, g := range p.Forwards {
		if g.Validate() != nil || g.Target != local || g.TargetPin != localPin || g.CreditNonce != "" && p.CreditLedgerID == "" {
			return errors.New("invalid local TCP service grant")
		}
		if g.CreditNonce != "" {
			if creditNonces[g.CreditNonce] {
				return errors.New("duplicate TCP credit nonce")
			}
			creditNonces[g.CreditNonce] = true
		}
		key := g.Workspace + ":" + g.Generation + ":" + g.Source + ":" + g.Endpoint.Name
		if seen[key] {
			return errors.New("duplicate exact TCP service grant")
		}
		seen[key] = true
	}
	clear(seen)
	for _, g := range p.Destinations {
		if err := g.Validate(); err != nil || g.Target != local || g.TargetPin != localPin {
			return errors.New("invalid local routed SSH destination grant")
		}
		key := g.Workspace + ":" + g.Generation + ":" + g.Source
		if seen[key] {
			return errors.New("duplicate original-source routed SSH grant")
		}
		seen[key] = true
	}
	var prior int64
	for _, e := range p.Events {
		if e.Revision <= prior || e.Revision > p.Revision || e.Action != "grant" && e.Action != "revoke" ||
			len(e.Reason) == 0 || len(e.Reason) > 4096 || !hexBytes(e.BeforeSHA256, 32) || !hexBytes(e.AfterSHA256, 32) || e.At.IsZero() {
			return errors.New("invalid bounded routed SSH policy audit")
		}
		prior = e.Revision
	}
	raw, _ := json.Marshal(p)
	if len(raw) > maxPolicyBytes {
		return errors.New("routed SSH policy exceeds byte bound")
	}
	return nil
}

func (p Policy) Relay(o Open) (RelayGrant, error) {
	for _, g := range p.Relays {
		if g.Workspace == o.Workspace && g.Generation == o.Generation && g.Local == o.ImmediateTarget() {
			return g, g.Authorize(time.Now(), o)
		}
	}
	return RelayGrant{}, errors.New("no reviewed workspace transport grant")
}

func (p Policy) Destination(o Open) (DestinationGrant, error) {
	for _, g := range p.Destinations {
		if g.Workspace == o.Workspace && g.Generation == o.Generation && g.Source == o.Source && g.Target == o.Target {
			if e := g.Validate(); e != nil || !g.Expires.After(time.Now()) || g.SourceGeneration != o.SourceGeneration ||
				g.TargetGeneration != o.TargetGeneration || g.SourcePin != o.SourcePin || g.TargetPin != o.TargetPin ||
				g.DirectoryPin != o.DirectoryPin || g.DirectorySPKI != o.DirectorySPKI ||
				o.Class != ClassSSHIdentity && g.SSHHostKeyPin != o.SSHHostKeyPin ||
				(o.Class == ClassSSH || o.Class == ClassSSHIdentity) && !g.SSH || o.Class == ClassSync && !g.Sync {
				return DestinationGrant{}, errors.New("reviewed routed SSH destination grant changed")
			}
			return g, nil
		}
	}
	return DestinationGrant{}, errors.New("no reviewed original-source routed SSH destination grant")
}

func (p Policy) Forward(o Open) (meshforward.Grant, error) {
	if o.Class != ClassForward || o.Validate(time.Now()) != nil {
		return meshforward.Grant{}, errors.New("invalid TCP route")
	}
	for _, g := range p.Forwards {
		if g.Workspace == o.Workspace && g.Generation == o.Generation && g.Source == o.Source && g.Target == o.Target && g.Endpoint.Name == o.Service {
			if g.ValidateCredit() != nil || p.CreditLedgerID == "" || !time.Now().Before(g.Expires) || g.SourceGeneration != o.SourceGeneration || g.TargetGeneration != o.TargetGeneration ||
				g.DirectoryPin != o.DirectoryPin || g.DirectorySPKI != o.DirectorySPKI || g.SourcePin != o.SourcePin || g.TargetPin != o.TargetPin {
				return meshforward.Grant{}, errors.New("exact TCP service grant changed")
			}
			return g, nil
		}
	}
	return meshforward.Grant{}, errors.New("no reviewed original-source TCP service grant")
}

// ReadPolicy reads one owned no-follow file. A missing file has no authority.
func ReadPolicy(home, local, localPin string) (Policy, error) {
	d, err := policyDirectory(home)
	if err != nil {
		return Policy{}, err
	}
	defer d.Close()
	return readPolicyAt(d, local, localPin)
}

func readPolicyAt(d *os.File, local, localPin string) (Policy, error) {
	fd, err := unix.Openat(int(d.Fd()), policyName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return Policy{}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	f := os.NewFile(uintptr(fd), policyName)
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return Policy{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&077 != 0 || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Size < 1 || st.Size > maxPolicyBytes {
		return Policy{}, errors.New("routed SSH policy file ownership, type or bound differs")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxPolicyBytes+1))
	if err != nil || len(raw) != int(st.Size) {
		return Policy{}, errors.New("routed SSH policy changed while reading")
	}
	var after unix.Stat_t
	if err = unix.Fstat(fd, &after); err != nil || after.Dev != st.Dev || after.Ino != st.Ino || after.Size != st.Size || after.Mtim != st.Mtim {
		return Policy{}, errors.New("routed SSH policy changed while reading")
	}
	var p Policy
	if err = workspace.Decode(bytes.TrimSpace(raw), &p); err != nil {
		return Policy{}, err
	}
	return p, p.Validate(local, localPin)
}

func policyDirectory(home string) (*os.File, error) {
	path := filepath.Join(home, "mesh")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	d := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&077 != 0 || st.Uid != uint32(os.Geteuid()) {
		d.Close()
		return nil, errors.New("private mesh policy directory authority differs")
	}
	return d, nil
}

// Review is an immutable compare-and-swap proposal. The caller displays the
// exact next grant/pins/effect and obtains native operator confirmation.
type Review struct {
	Before       Policy
	After        Policy
	BeforeSHA256 string
	AfterSHA256  string
	Home         string
}

func PolicyDigest(p Policy) string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ReviewPolicy(home, local, localPin, action, reason string, change func(*Policy) error) (Review, error) {
	if action != "grant" && action != "revoke" || len(reason) == 0 || len(reason) > 4096 || change == nil {
		return Review{}, errors.New("policy change requires bounded operator reason")
	}
	before, err := ReadPolicy(home, local, localPin)
	if err != nil {
		return Review{}, err
	}
	if before.Schema == 0 {
		before = Policy{Schema: 1, Local: local, LocalPin: localPin, Revision: 1, Relays: []RelayGrant{}, Destinations: []DestinationGrant{}, Events: []PolicyEvent{}}
	}
	after := before
	after.Relays = append([]RelayGrant{}, before.Relays...)
	after.Destinations = append([]DestinationGrant{}, before.Destinations...)
	after.Forwards = append([]meshforward.Grant{}, before.Forwards...)
	after.Events = append([]PolicyEvent{}, before.Events...)
	if err = change(&after); err != nil {
		return Review{}, err
	}
	if after.Revision != before.Revision || after.Local != before.Local || after.LocalPin != before.LocalPin ||
		after.CreditLedgerID != before.CreditLedgerID || !reflect.DeepEqual(after.Events, before.Events) {
		return Review{}, errors.New("policy edit may change only reviewed grants")
	}
	if after.CreditLedgerID == "" {
		for _, g := range after.Forwards {
			if g.CreditNonce != "" {
				after.CreditLedgerID, err = meshforward.NewCreditNonce()
				if err != nil {
					return Review{}, err
				}
				break
			}
		}
	}
	if PolicyDigest(after) == PolicyDigest(before) {
		return Review{}, errors.New("routed SSH policy unchanged")
	}
	if before.Revision == int64(^uint64(0)>>1) {
		return Review{}, errors.New("policy revision exhausted")
	}
	after.Revision++
	beforeDigest := PolicyDigest(before)
	afterDigest := PolicyDigest(after)
	after.Events = append(after.Events, PolicyEvent{Revision: after.Revision, Action: action, Reason: reason, BeforeSHA256: beforeDigest, AfterSHA256: afterDigest, At: time.Now().UTC()})
	if err = after.Validate(local, localPin); err != nil {
		return Review{}, err
	}
	return Review{Before: before, After: after, BeforeSHA256: beforeDigest, AfterSHA256: PolicyDigest(after), Home: home}, nil
}

// CommitPolicy performs the reviewed local change under a cross-process lock.
// It never grants anything if the recorded before image has changed.
func CommitPolicy(r Review) error {
	if r.After.Validate(r.Before.Local, r.Before.LocalPin) != nil || PolicyDigest(r.Before) != r.BeforeSHA256 || PolicyDigest(r.After) != r.AfterSHA256 {
		return errors.New("routed SSH policy review changed")
	}
	d, err := policyDirectory(r.Home)
	if err != nil {
		return err
	}
	defer d.Close()
	lockFD, err := unix.Openat(int(d.Fd()), policyLockName, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(lockFD)
	if err = unix.Flock(lockFD, unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(lockFD, unix.LOCK_UN)
	var lockStat unix.Stat_t
	if err = unix.Fstat(lockFD, &lockStat); err != nil || lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Mode&077 != 0 || lockStat.Nlink != 1 || lockStat.Uid != uint32(os.Geteuid()) {
		return errors.New("routed SSH policy lock authority differs")
	}
	current, err := readPolicyAt(d, r.Before.Local, r.Before.LocalPin)
	if err != nil {
		return err
	}
	if current.Schema == 0 {
		current = Policy{Schema: 1, Local: r.Before.Local, LocalPin: r.Before.LocalPin, Revision: 1, Relays: []RelayGrant{}, Destinations: []DestinationGrant{}, Events: []PolicyEvent{}}
	}
	if PolicyDigest(current) != r.BeforeSHA256 {
		return errors.New("routed SSH policy changed since review")
	}
	if err = prepareForwardCreditsAt(d, current, r.After); err != nil {
		return err
	}
	raw, err := json.Marshal(r.After)
	if err != nil {
		return err
	}
	name := policyName + "." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".tmp"
	fd, err := unix.Openat(int(d.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer unix.Unlinkat(int(d.Fd()), name, 0)
	if _, err = f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(int(d.Fd()), name, int(d.Fd()), policyName); err != nil {
		return err
	}
	if err = unix.Fsync(int(d.Fd())); err != nil {
		return fmt.Errorf("routed SSH policy committed but directory sync unconfirmed: %w", err)
	}
	return nil
}

// WithPolicyRead serializes a destination's final admission cut against local
// grant revocation. The callback must be bounded and must not perform network
// I/O or recursively try to commit this policy.
func WithPolicyRead(home, local, localPin string, fn func(Policy) error) error {
	if fn == nil {
		return errors.New("missing routed SSH admission callback")
	}
	d, err := policyDirectory(home)
	if err != nil {
		return err
	}
	defer d.Close()
	fd, err := unix.Openat(int(d.Fd()), policyLockName, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_SH); err != nil {
		return err
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	var lockStat unix.Stat_t
	if err = unix.Fstat(fd, &lockStat); err != nil || lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Mode&077 != 0 || lockStat.Nlink != 1 || lockStat.Uid != uint32(os.Geteuid()) {
		return errors.New("routed SSH policy lock authority differs")
	}
	p, err := readPolicyAt(d, local, localPin)
	if err != nil {
		return err
	}
	return fn(p)
}
