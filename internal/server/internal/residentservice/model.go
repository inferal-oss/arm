// Package residentservice owns only the original resident core's standing debit.
// Stored records are data; they do not issue native or service capabilities.
package residentservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Build struct {
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	GOOS     string `json:"goos"`
	GOARCH   string `json:"goarch"`
	GoBuild  string `json:"go_build"`
	Protocol uint32 `json:"protocol"`
}

type Resources struct {
	CPU    float64 `json:"cpu"`
	Memory int64   `json:"memory"`
	Disk   int64   `json:"disk"`
}

type Bounds struct {
	Tasks, Descriptors, Children, Sockets, NativePending uint32
	IOBytes, RetainedReceiptBytes                        uint64
}

type MeasuredProfile struct {
	Schema             uint32                `json:"schema"`
	Build              Build                 `json:"build"`
	Resources          Resources             `json:"resources"`
	Bounds             Bounds                `json:"bounds"`
	EvidenceSHA256     string                `json:"evidence_sha256"`
	RuntimeEnvironment *ExecutionEnvironment `json:"runtime_environment,omitempty"`
}

type NativeID struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
}

type OperatorIntent struct {
	Key              string          `json:"key"`
	Home             string          `json:"home"`
	HomeID           NativeID        `json:"home_id"`
	ExpectedRevision uint64          `json:"expected_revision"`
	Profile          MeasuredProfile `json:"profile"`
	Reason           string          `json:"reason"`
	Inputs           OperatorInputs  `json:"inputs"`
}

type GrantRecord struct {
	ID       string         `json:"id"`
	Revision uint64         `json:"revision"`
	PlanSHA  string         `json:"plan_sha256"`
	Intent   OperatorIntent `json:"intent"`
}

type DebitRecord struct {
	ID               string   `json:"id"`
	LaunchKey        string   `json:"launch_key"`
	GrantID          string   `json:"grant_id"`
	GrantRevision    uint64   `json:"grant_revision"`
	ProfileSHA256    string   `json:"profile_sha256"`
	SecretCommitment string   `json:"secret_commitment"`
	LockID           NativeID `json:"lock_id"`
	JournalID        NativeID `json:"journal_id"`
}

type RootBoundRecord struct {
	DebitID          string `json:"debit_id"`
	LaunchKey        string `json:"launch_key"`
	Generation       string `json:"generation"`
	CommitmentSHA256 string `json:"commitment_sha256"`
}

type Record struct {
	Schema   uint32           `json:"schema"`
	Sequence uint32           `json:"sequence"`
	Kind     string           `json:"kind"`
	Grant    *GrantRecord     `json:"grant,omitempty"`
	Debit    *DebitRecord     `json:"debit,omitempty"`
	Root     *RootBoundRecord `json:"root,omitempty"`
}

var ErrInvalid = errors.New("invalid resident standing-custody record; preserve original evidence")
var ErrUnresolved = errors.New("original resident debit remains unresolved; no replacement or release is authorized")

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func boundedText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (p MeasuredProfile) Validate() error {
	if (p.Schema != 1 && p.Schema != 2) || !validDigest(p.Build.SHA256) || p.Build.Size <= 0 || p.Build.Size > 1<<30 || p.Build.Protocol != 1 || !boundedText(p.Build.GoBuild, 256) || !validDigest(p.EvidenceSHA256) {
		return ErrInvalid
	}
	if p.Build.GOOS != "linux" && p.Build.GOOS != "darwin" || p.Build.GOARCH != "amd64" && p.Build.GOARCH != "arm64" {
		return ErrInvalid
	}
	if math.IsNaN(p.Resources.CPU) || math.IsInf(p.Resources.CPU, 0) || p.Resources.CPU <= 0 || p.Resources.Memory <= 0 || p.Resources.Disk <= 0 {
		return ErrInvalid
	}
	if p.Schema == 1 && p.RuntimeEnvironment != nil {
		return ErrInvalid
	}
	if p.Schema == 2 {
		if p.RuntimeEnvironment == nil {
			return ErrInvalid
		}
		heap, err := p.RuntimeEnvironment.Validate()
		if err != nil || heap > p.Resources.Memory {
			return ErrInvalid
		}
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > MaxProfileBytes {
		return ErrInvalid
	}
	b := p.Bounds
	if b.Tasks == 0 || b.Descriptors == 0 || b.Children == 0 || b.Sockets == 0 || b.NativePending == 0 || b.IOBytes == 0 || b.RetainedReceiptBytes == 0 {
		return ErrInvalid
	}
	return nil
}

func (p MeasuredProfile) SHA256() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func (i OperatorIntent) Validate() error {
	if !boundedText(i.Key, 128) || !boundedText(i.Reason, 1024) || !filepath.IsAbs(i.Home) || filepath.Clean(i.Home) != i.Home || i.Home == "/" || len(i.Home) > 4096 || strings.ContainsRune(i.Home, 0) || i.HomeID.Inode == 0 {
		return ErrInvalid
	}
	if err := i.Inputs.Validate(); err != nil {
		return err
	}
	profileSHA, err := i.Profile.SHA256()
	if err != nil || i.Inputs.Profile.SHA256 != profileSHA || i.Inputs.Evidence.SHA256 != i.Profile.EvidenceSHA256 || i.Inputs.Profile.ID.UID != i.HomeID.UID || i.Inputs.Evidence.ID.UID != i.HomeID.UID || i.Inputs.Profile.Path == i.Inputs.Evidence.Path {
		return ErrInvalid
	}
	encoded, err := json.Marshal(i.Profile)
	if err != nil || int64(len(encoded)) != i.Inputs.Profile.Size {
		return ErrInvalid
	}
	return nil
}

func (i OperatorIntent) SHA256() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

// ValidateLedger returns no authority. Only the actual issuer's retained native
// journal and original append/handoff can issue opaque charge/debit handles.
func ValidateLedger(records []Record) error {
	if len(records) == 0 || len(records) > 3 {
		return ErrInvalid
	}
	first := records[0]
	if first.Schema != 1 || first.Sequence != 1 || first.Kind != "operator-authorized" || first.Grant == nil || first.Debit != nil || first.Root != nil {
		return ErrInvalid
	}
	g := first.Grant
	if !validDigest(g.ID) || g.Revision != 1 || g.Intent.ExpectedRevision != 0 {
		return ErrInvalid
	}
	plan, err := g.Intent.SHA256()
	if err != nil || plan != g.PlanSHA {
		return ErrInvalid
	}
	if len(records) == 1 {
		return nil
	}
	second := records[1]
	if second.Schema != 1 || second.Sequence != 2 || second.Kind != "debit-prepared" || second.Debit == nil || second.Grant != nil || second.Root != nil {
		return ErrInvalid
	}
	d := second.Debit
	profile, err := g.Intent.Profile.SHA256()
	if err != nil || !validDigest(d.ID) || !boundedText(d.LaunchKey, 128) || d.GrantID != g.ID || d.GrantRevision != g.Revision || d.ProfileSHA256 != profile || !validDigest(d.SecretCommitment) || d.LockID.Inode == 0 || d.JournalID.Inode == 0 || d.LockID.UID != g.Intent.HomeID.UID || d.JournalID.UID != g.Intent.HomeID.UID {
		return ErrInvalid
	}
	if len(records) == 2 {
		return nil
	}
	third := records[2]
	if third.Schema != 1 || third.Sequence != 3 || third.Kind != "root-bound" || third.Root == nil || third.Grant != nil || third.Debit != nil {
		return ErrInvalid
	}
	root := third.Root
	_, generationErr := hex.DecodeString(root.Generation)
	if root.DebitID != d.ID || root.LaunchKey != d.LaunchKey || len(root.Generation) != 32 || generationErr != nil || strings.ToLower(root.Generation) != root.Generation || !validDigest(root.CommitmentSHA256) {
		return ErrInvalid
	}
	return nil
}
