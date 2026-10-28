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
const Start byte = 't'
const Bootstrap byte = 'e'
const Forward byte = 'f'

type Role string

const SourceRole Role = "source"
const StorageRole Role = "storage"
const PortRole Role = "port"

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
var portNamespace = regexp.MustCompile(`^(host|linux:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:net:\[[1-9][0-9]{0,19}\])$`)

type Scope struct {
	Operation, Attempt string
	Stage              byte
	B                  int64
}

func (s Scope) Validate() error {
	if !hex64.MatchString(s.Operation) || !hex32.MatchString(s.Attempt) || (s.Stage != Ship && s.Stage != Inspect && s.Stage != Start && s.Stage != Bootstrap && s.Stage != Forward) || s.B < 1 || s.B > MaxPayload {
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
	s, role, err := ParseRole(key)
	if role == PortRole {
		return Scope{}, false, fmt.Errorf("source port role requires explicit role parser")
	}
	return s, role == StorageRole, err
}
func (s Scope) PortKey() string { return "kp1" + s.Key(false)[3:] }
func ParseRole(key string) (Scope, Role, error) {
	var s Scope
	if len(key) > 128 {
		return s, "", fmt.Errorf("source key exceeds bound")
	}
	v := strings.Split(key, ":")
	if len(v) != 5 || (v[0] != "ks1" && v[0] != "kd1" && v[0] != "kp1") || len(v[3]) != 1 {
		return s, "", fmt.Errorf("invalid source pair key")
	}
	b, err := strconv.ParseInt(v[4], 10, 64)
	if err != nil {
		return s, "", fmt.Errorf("invalid source size")
	}
	s = Scope{v[1], v[2], v[3][0], b}
	role, canonical := SourceRole, s.Key(false)
	if v[0] == "kd1" {
		role, canonical = StorageRole, s.Key(true)
	}
	if v[0] == "kp1" {
		role, canonical = PortRole, s.PortKey()
	}
	if s.Validate() != nil || canonical != key || role == PortRole && s.Stage != Forward {
		return Scope{}, "", fmt.Errorf("noncanonical source pair key")
	}
	return s, role, nil
}
func IsReserved(r model.Request) bool {
	return r.Project == Project || strings.HasPrefix(r.Key, "ks1:") || strings.HasPrefix(r.Key, "kd1:") || strings.HasPrefix(r.Key, "kp1:")
}

func (s Scope) ContentBytes() int64 {
	if s.Stage == Inspect {
		return 4 * RequestBytes
	}
	if s.Stage == Start {
		return 3 * RequestBytes
	}
	if s.Stage == Bootstrap || s.Stage == Forward {
		return RequestBytes
	}
	return s.B + 1 + 3*RequestBytes
}
func (s Scope) DiskBytes() int64 { return ((s.ContentBytes()+4095)/4096)*4096 + 65536 }
func (s Scope) Purposes() []string {
	if s.Stage == Inspect {
		return []string{"lookup_binary", "catalog_proof", "lookup_start_marker", "runtime_proof"}
	}
	if s.Stage == Start {
		return []string{"catalog_proof", "start_marker", "lookup_start_marker"}
	}
	if s.Stage == Bootstrap {
		return []string{"mesh_bootstrap"}
	}
	if s.Stage == Forward {
		return []string{"mesh_forward"}
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

func (s Scope) PortRequest(host, budget, namespace string) model.Request {
	return model.Request{Host: host, Budget: budget, Project: Project, Key: s.PortKey(), Reason: "Kubernetes source port f", Kind: "ports", TTL: ParentTTL, Ports: []model.PortClaim{{Name: "kubernetes-mesh", Network: "tcp4", Address: "127.0.0.1", Mode: "hold", Namespace: namespace}}}
}

// ValidateRole includes every request field; only the source's one attachment
// is permitted to vary. Normalized nil/empty slices are the ordinary Go form.
func ValidateRole(l model.Lease) (Scope, bool, error) {
	s, role, err := ValidateRoleKind(l)
	return s, role != SourceRole, err
}
func ValidateRoleKind(l model.Lease) (Scope, Role, error) {
	s, role, err := ParseRole(l.Request.Key)
	if err != nil {
		return s, role, err
	}
	// These fixed profiles own only the default storage root. A saved named
	// binding, including a zero-value binding, is not an equivalent authority.
	if l.StorageBinding != nil {
		return s, role, fmt.Errorf("source role carries a non-default storage binding")
	}
	if l.Host == "" || l.Owner == "" || !hex32.MatchString(l.ID) || l.PID != 0 || l.Process != nil {
		return s, role, fmt.Errorf("invalid source pair identity")
	}
	want := s.Request(l.Host, l.Request.Budget, role == StorageRole)
	if role == PortRole {
		if len(l.Request.Ports) != 1 || !portNamespace.MatchString(l.Request.Ports[0].Namespace) {
			return s, role, fmt.Errorf("source port namespace observation is noncanonical")
		}
		want = s.PortRequest(l.Host, l.Request.Budget, l.Request.Ports[0].Namespace)
	} else if len(l.Ports) != 0 {
		return s, role, fmt.Errorf("non-port source role carries native port bindings")
	}
	if role == SourceRole {
		want.Kind = "relay-transit"
		want.StorageLease = l.Request.StorageLease
		if s.Stage == Forward {
			want.PortLease = l.Request.PortLease
			if want.PortLease != "" && want.StorageLease == "" {
				return s, role, fmt.Errorf("source port attachment precedes storage attachment")
			}
		}
		if l.Artifact != nil || l.ManagedDir != "" {
			return s, role, fmt.Errorf("source has storage artifact")
		}
	}
	if !reflect.DeepEqual(want, l.Request) {
		return s, role, fmt.Errorf("noncanonical source request")
	}
	return s, role, nil
}
func ValidatePair(source, parent model.Lease) error {
	a, ar, err := ValidateRoleKind(source)
	if err != nil || ar != SourceRole {
		return fmt.Errorf("invalid source consumer")
	}
	b, br, err := ValidateRoleKind(parent)
	if err != nil || br != StorageRole || a != b || source.ID == parent.ID || source.Request.StorageLease != parent.ID || source.Host != parent.Host || source.Owner != parent.Owner || source.Request.Budget != parent.Request.Budget {
		return fmt.Errorf("source parent binding differs")
	}
	uid, uidErr := strconv.ParseUint(parent.Owner, 10, 32)
	if parent.Artifact == nil || parent.Artifact.Identity == nil || parent.Artifact.Name != parent.ID || parent.ManagedDir == "" || uidErr != nil || strconv.FormatUint(uid, 10) != parent.Owner || parent.Artifact.Identity.Owner != uint32(uid) || !parent.Artifact.Identity.BirthKnown || parent.Artifact.Identity.Inode == 0 {
		return fmt.Errorf("source parent lacks owned artifact")
	}
	return nil
}

func ValidatePortPair(source, parent model.Lease) error {
	a, ar, err := ValidateRoleKind(source)
	if err != nil || ar != SourceRole || a.Stage != Forward {
		return fmt.Errorf("invalid forward source consumer")
	}
	b, br, err := ValidateRoleKind(parent)
	if err != nil || br != PortRole || a != b || source.ID == parent.ID || source.Request.PortLease != parent.ID || source.Host != parent.Host || source.Owner != parent.Owner || source.Request.Budget != parent.Request.Budget || parent.Artifact != nil || parent.ManagedDir != "" || len(parent.Ports) != 1 {
		return fmt.Errorf("source port parent binding differs")
	}
	binding := parent.Ports[0]
	want := parent.Request.Ports[0]
	want.Port = binding.Port
	if binding.Port < 1 || binding.Port > 65535 || binding.PortClaim != want {
		return fmt.Errorf("source port parent lacks exact native binding")
	}
	return nil
}
