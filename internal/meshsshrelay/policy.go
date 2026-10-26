package meshsshrelay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
)

// RelayGrant is explicit local opt-in for SSH/sync forwarding in one exact
// workspace generation. It is independent of messaging relay membership.
type RelayGrant struct {
	Workspace  string    `json:"workspace"`
	Generation string    `json:"generation"`
	Local      string    `json:"local"`
	Revision   int       `json:"revision"`
	SSH        bool      `json:"ssh"`
	Sync       bool      `json:"sync"`
	Expires    time.Time `json:"expires_at"`
}

func (g RelayGrant) Authorize(now time.Time, o Open) error {
	if err := o.Validate(now); err != nil {
		return err
	}
	if g.Workspace != o.Workspace || g.Generation != o.Generation || g.Local != o.ImmediateTarget() ||
		g.Revision < 1 || !g.Expires.After(now) || o.Final() ||
		((o.Class == ClassSSH || o.Class == ClassSSHIdentity) && !g.SSH) || (o.Class == ClassSync && !g.Sync) {
		return errors.New("separate current SSH relay opt-in unavailable")
	}
	return nil
}

// DestinationGrant belongs to the final host. Source identity is the verified
// INNER TLS client, never an immediate outer relay. It deliberately names the
// account, SSH host key, source/destination SPKI and resource ceilings.
type DestinationGrant struct {
	Workspace        string          `json:"workspace"`
	Generation       string          `json:"generation"`
	Source           string          `json:"source"`
	Target           string          `json:"target"`
	SourceGeneration int64           `json:"source_generation"`
	TargetGeneration int64           `json:"target_generation"`
	DirectoryPin     string          `json:"directory_pin"`
	DirectorySPKI    string          `json:"directory_spki"`
	SourcePin        string          `json:"source_pin"`
	SourceSPKI       string          `json:"source_spki"`
	TargetPin        string          `json:"target_pin"`
	TargetSPKI       string          `json:"target_spki"`
	SSHHostKeyPin    string          `json:"ssh_host_key_pin"`
	Account          string          `json:"account"`
	Owner            string          `json:"owner"`
	Revision         int             `json:"revision"`
	SSH              bool            `json:"ssh"`
	Sync             bool            `json:"sync"`
	MaxTTL           time.Duration   `json:"max_ttl_ns"`
	Limits           model.Resources `json:"limits"`
	Expires          time.Time       `json:"expires_at"`
}

func (g DestinationGrant) Validate() error {
	uid, e := strconv.ParseUint(g.Owner, 10, 32)
	accountOK := utf8.ValidString(g.Account) && len(g.Account) > 0 && len(g.Account) <= 256
	for _, r := range g.Account {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			accountOK = false
		}
	}
	if !hexBytes(g.Workspace, 16) || !hexBytes(g.Generation, 16) || !hexBytes(g.Source, 16) || !hexBytes(g.Target, 16) ||
		g.Source == g.Target || g.SourceGeneration < 1 || g.TargetGeneration < 1 ||
		!hexBytes(g.DirectoryPin, 32) || !hexBytes(g.DirectorySPKI, 32) ||
		!hexBytes(g.SourcePin, 32) || !hexBytes(g.SourceSPKI, 32) || !hexBytes(g.TargetPin, 32) ||
		!hexBytes(g.TargetSPKI, 32) || !hexBytes(g.SSHHostKeyPin, 32) || g.Revision < 1 ||
		e != nil || strconv.FormatUint(uid, 10) != g.Owner || !accountOK ||
		!g.SSH && !g.Sync || g.MaxTTL <= 0 || g.MaxTTL > 7*24*time.Hour ||
		math.IsNaN(g.Limits.CPU) || math.IsInf(g.Limits.CPU, 0) || g.Limits.CPU <= 0 || g.Limits.CPU > 65536 ||
		g.Limits.Memory < 0 || g.Limits.Disk < 0 || g.Expires.IsZero() {
		return errors.New("invalid exact routed SSH destination grant")
	}
	return nil
}

func (g DestinationGrant) Authorize(now time.Time, o Open, originalSourcePin, originalSourceSPKI, localTargetPin, localTargetSPKI, currentSSHHostKey, account, owner string, ttl time.Duration, resources model.Resources) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if err := o.Validate(now); err != nil {
		return err
	}
	if !o.Final() || g.Workspace != o.Workspace || g.Generation != o.Generation ||
		g.Source != o.Source || g.Target != o.Target || g.SourceGeneration != o.SourceGeneration || g.TargetGeneration != o.TargetGeneration ||
		g.DirectoryPin != o.DirectoryPin || g.DirectorySPKI != o.DirectorySPKI ||
		g.SourcePin != o.SourcePin || g.TargetPin != o.TargetPin || g.SSHHostKeyPin != o.SSHHostKeyPin ||
		g.SourcePin != originalSourcePin || g.SourceSPKI != originalSourceSPKI ||
		g.TargetPin != localTargetPin || g.TargetSPKI != localTargetSPKI ||
		g.SSHHostKeyPin != currentSSHHostKey || g.Account != account || g.Owner != owner ||
		!g.Expires.After(now) || ttl <= 0 || ttl > g.MaxTTL || !resources.Fits(g.Limits) ||
		((o.Class == ClassSSH || o.Class == ClassSSHIdentity) && !g.SSH) || (o.Class == ClassSync && !g.Sync) {
		return errors.New("current original-source routed SSH grant differs")
	}
	return nil
}

// VirtualPair is a stable operation namespace for the exact routed endpoint
// grant, never the immediate relay's physical pair. It lets the existing SSH
// key ledger preserve original-source dedup and lookup across path changes.
func (g DestinationGrant) VirtualPair() (string, error) {
	if err := g.Validate(); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(struct {
		Tag, Workspace, Generation, Source, Target                                                               string
		SourceGeneration, TargetGeneration                                                                       int64
		DirectoryPin, DirectorySPKI, SourcePin, SourceSPKI, TargetPin, TargetSPKI, SSHHostKeyPin, Account, Owner string
		Revision                                                                                                 int
	}{"arm.routed.ssh.grant.v1", g.Workspace, g.Generation, g.Source, g.Target, g.SourceGeneration, g.TargetGeneration,
		g.DirectoryPin, g.DirectorySPKI, g.SourcePin, g.SourceSPKI, g.TargetPin, g.TargetSPKI, g.SSHHostKeyPin, g.Account, g.Owner, g.Revision})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
