// Package kubesource describes the closed native Kubernetes source resource
// pair. Descriptions and durable rows never confer an original runtime handle.
package kubesource

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
)

const Project = "kube-source-v1"
const MaxPayload int64 = 256 << 20
const RequestBytes int64 = 65536
const SourceTTL = 5 * time.Minute
const ParentTTL = 6 * time.Minute
const SourceMemory int64 = 128 << 20
const SourceCPU = .1
const Ship byte = 's'
const Inspect byte = 'i'

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Scope struct {
	Operation, Attempt string
	Stage              byte
	B                  int64
}

func (s Scope) Validate() error {
	if !hex64.MatchString(s.Operation) || !hex32.MatchString(s.Attempt) || (s.Stage != Ship && s.Stage != Inspect) || s.B < 1 || s.B > MaxPayload {
		return fmt.Errorf("invalid Kubernetes source scope")
	}
	return nil
}
func (s Scope) Key(parent bool) string {
	role := "ks1"
	if parent {
		role = "kd1"
	}
	return fmt.Sprintf("%s:%s:%s:%c:%d", role, s.Operation, s.Attempt, s.Stage, s.B)
}
func Parse(key string) (Scope, bool, error) {
	var s Scope
	if len(key) > 128 {
		return s, false, fmt.Errorf("source key exceeds bound")
	}
	v := strings.Split(key, ":")
	if len(v) != 5 || (v[0] != "ks1" && v[0] != "kd1") || len(v[3]) != 1 {
		return s, false, fmt.Errorf("invalid source pair key")
	}
	b, err := strconv.ParseInt(v[4], 10, 64)
	if err != nil {
		return s, false, fmt.Errorf("invalid source size")
	}
	s = Scope{v[1], v[2], v[3][0], b}
	parent := v[0] == "kd1"
	if s.Validate() != nil || s.Key(parent) != key {
		return Scope{}, false, fmt.Errorf("noncanonical source pair key")
	}
	return s, parent, nil
}
func IsReserved(r model.Request) bool {
	return r.Project == Project || strings.HasPrefix(r.Key, "ks1:") || strings.HasPrefix(r.Key, "kd1:")
}

func (s Scope) ContentBytes() int64 {
	if s.Stage == Inspect {
		return 4 * RequestBytes
	}
	return s.B + 1 + 3*RequestBytes
}
func (s Scope) DiskBytes() int64 { return ((s.ContentBytes()+4095)/4096)*4096 + 65536 }
func (s Scope) Purposes() []string {
	if s.Stage == Inspect {
		return []string{"lookup_binary", "catalog_proof", "lookup_start_marker", "runtime_proof"}
	}
	return []string{"payload", "ship", "lookup_binary", "catalog_proof"}
}
func (s Scope) Limit(purpose string) (int64, error) {
	if s.Validate() != nil {
		return 0, fmt.Errorf("invalid source scope")
	}
	for _, p := range s.Purposes() {
		if p == purpose {
			if p == "payload" {
				return s.B + 1, nil
			}
			return RequestBytes, nil
		}
	}
	return 0, fmt.Errorf("purpose unavailable in source profile")
}
func (s Scope) Request(host, budget string, parent bool) model.Request {
	r := model.Request{Host: host, Budget: budget, Project: Project, Key: s.Key(parent), Reason: "Kubernetes source stage " + string(s.Stage), Resources: model.Resources{CPU: SourceCPU, Memory: SourceMemory}, TTL: SourceTTL}
	if parent {
		r.Kind = "storage"
		r.Reason = "Kubernetes source scratch " + string(s.Stage)
		r.Resources = model.Resources{Disk: s.DiskBytes()}
		r.TTL = ParentTTL
	}
	return r
}

// ValidateRole includes every request field; only the source's one attachment
// is permitted to vary. Normalized nil/empty slices are the ordinary Go form.
func ValidateRole(l model.Lease) (Scope, bool, error) {
	s, parent, err := Parse(l.Request.Key)
	if err != nil {
		return s, parent, err
	}
	if l.Host == "" || l.Owner == "" || !hex32.MatchString(l.ID) || l.PID != 0 || l.Process != nil {
		return s, parent, fmt.Errorf("invalid source pair identity")
	}
	want := s.Request(l.Host, l.Request.Budget, parent)
	if !parent {
		want.Kind = "relay-transit"
		want.StorageLease = l.Request.StorageLease
		if l.Artifact != nil || l.ManagedDir != "" {
			return s, parent, fmt.Errorf("source has storage artifact")
		}
	}
	if !reflect.DeepEqual(want, l.Request) {
		return s, parent, fmt.Errorf("noncanonical source request")
	}
	return s, parent, nil
}
func ValidatePair(source, parent model.Lease) error {
	a, ap, err := ValidateRole(source)
	if err != nil || ap {
		return fmt.Errorf("invalid source consumer")
	}
	b, bp, err := ValidateRole(parent)
	if err != nil || !bp || a != b || source.ID == parent.ID || source.Request.StorageLease != parent.ID || source.Host != parent.Host || source.Owner != parent.Owner || source.Request.Budget != parent.Request.Budget {
		return fmt.Errorf("source parent binding differs")
	}
	uid, uidErr := strconv.ParseUint(parent.Owner, 10, 32)
	if parent.Artifact == nil || parent.Artifact.Identity == nil || parent.Artifact.Name != parent.ID || parent.ManagedDir == "" || uidErr != nil || strconv.FormatUint(uid, 10) != parent.Owner || parent.Artifact.Identity.Owner != uint32(uid) || !parent.Artifact.Identity.BirthKnown || parent.Artifact.Identity.Inode == 0 {
		return fmt.Errorf("source parent lacks owned artifact")
	}
	return nil
}
