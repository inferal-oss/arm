// Package meshforward contains the separately reviewed authority and bounded
// byte-stream machinery for the ARM mesh TCP service route. Nothing in
// this package treats SSH, sync or workspace membership as a TCP grant.
package meshforward

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func NewCreditNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

const (
	MaxNodes       = 8
	MaxLifetime    = 15 * time.Minute
	MaxConnections = 8
	MaxBytes       = 8 << 30
	MaxRate        = 1 << 30
	MaxGrantBytes  = 8 << 30
)

// Endpoint names one destination-local service. The address is installed by
// that destination's operator; no route request can supply or replace it.
// Literal loopback IPs avoid DNS rebinding and accidental LAN exposure.
type Endpoint struct {
	Name    string `json:"name"`
	Network string `json:"network"`
	Address string `json:"address"`
}

func (e Endpoint) Validate() error {
	if len(e.Name) < 1 || len(e.Name) > 64 || e.Name[0] == '-' {
		return errors.New("invalid exact service name")
	}
	for _, r := range e.Name {
		if r != '-' && r != '_' && r != '.' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return errors.New("invalid exact service name")
		}
	}
	if e.Network != "tcp" {
		return errors.New("only TCP service endpoints are supported")
	}
	host, port, err := net.SplitHostPort(e.Address)
	if err != nil {
		return errors.New("service address must be an exact literal IP and port")
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("service endpoint must be a literal loopback IP and canonical port")
	}
	return nil
}

func hexID(s string, bytes int) bool {
	if len(s) != 2*bytes {
		return false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return false
	}
	for _, c := range s {
		if c >= 'A' && c <= 'F' {
			return false
		}
	}
	return true
}

// Grant is destination-local TCP authority for one original source and one
// exact installed service. A relay's own grant cannot be substituted.
type Grant struct {
	Workspace          string          `json:"workspace"`
	Generation         string          `json:"generation"`
	Source             string          `json:"source"`
	Target             string          `json:"target"`
	SourceGeneration   int64           `json:"source_generation"`
	TargetGeneration   int64           `json:"target_generation"`
	DirectoryPin       string          `json:"directory_pin"`
	DirectorySPKI      string          `json:"directory_spki"`
	SourcePin          string          `json:"source_pin"`
	SourceSPKI         string          `json:"source_spki"`
	TargetPin          string          `json:"target_pin"`
	TargetSPKI         string          `json:"target_spki"`
	Account            string          `json:"account"`
	Endpoint           Endpoint        `json:"endpoint"`
	Revision           int64           `json:"revision"`
	MaxConnections     int             `json:"max_connections"`
	BytesPerConnection int64           `json:"bytes_per_connection"`
	BytesPerSecond     int64           `json:"bytes_per_second"`
	TotalBytes         int64           `json:"total_bytes,omitempty"`
	CreditNonce        string          `json:"credit_nonce,omitempty"`
	Transit            model.Resources `json:"transit_resources"`
	MaxTTL             time.Duration   `json:"max_ttl_ns"`
	Expires            time.Time       `json:"expires_at"`
}

func (g Grant) Validate() error {
	if !hexID(g.Workspace, 16) || !hexID(g.Generation, 16) || !hexID(g.Source, 16) || !hexID(g.Target, 16) || g.Source == g.Target ||
		!hexID(g.DirectoryPin, 32) || !hexID(g.DirectorySPKI, 32) || !hexID(g.SourcePin, 32) || !hexID(g.SourceSPKI, 32) ||
		!hexID(g.TargetPin, 32) || !hexID(g.TargetSPKI, 32) || g.SourceGeneration < 1 || g.TargetGeneration < 1 ||
		g.Revision < 1 || g.MaxConnections < 1 || g.MaxConnections > MaxConnections ||
		g.BytesPerConnection < 1 || g.BytesPerConnection > MaxBytes || g.BytesPerSecond < 64<<10 || g.BytesPerSecond > MaxRate ||
		(g.TotalBytes != 0 || g.CreditNonce != "") && (g.TotalBytes < 1 || g.TotalBytes > MaxGrantBytes || !hexID(g.CreditNonce, 16)) ||
		g.MaxTTL <= 0 || g.MaxTTL > MaxLifetime || g.Expires.IsZero() || g.Endpoint.Validate() != nil ||
		math.IsNaN(g.Transit.CPU) || math.IsInf(g.Transit.CPU, 0) || g.Transit.CPU <= 0 || g.Transit.CPU > 64 || g.Transit.Memory < 1<<20 || g.Transit.Memory > 1<<30 || g.Transit.Disk != 0 ||
		len(g.Account) < 1 || len(g.Account) > 256 {
		return errors.New("invalid exact destination TCP service grant")
	}
	for _, c := range g.Account {
		if c <= ' ' || c == 127 {
			return errors.New("invalid destination account")
		}
	}
	return nil
}

// ValidateCredit distinguishes readable legacy policy records from grants that
// can open a new stream. A lifetime limit is never inferred from other limits.
func (g Grant) ValidateCredit() error {
	if err := g.Validate(); err != nil {
		return err
	}
	if g.TotalBytes == 0 || !hexID(g.CreditNonce, 16) {
		return errors.New("TCP grant needs an explicit reviewed total byte credit")
	}
	return nil
}

// Request is authenticated by the pinned inner TLS channel. It has no dial
// address and no SSH or sync selector.
type Request struct {
	Version          int           `json:"schema_version"`
	Workspace        string        `json:"workspace"`
	Generation       string        `json:"generation"`
	Source           string        `json:"source"`
	Target           string        `json:"target"`
	SourcePin        string        `json:"source_pin"`
	TargetPin        string        `json:"target_pin"`
	Service          string        `json:"service"`
	GrantRevision    int64         `json:"grant_revision"`
	ConnectionID     string        `json:"connection_id"`
	TTL              time.Duration `json:"ttl_ns"`
	RouteBindingHash string        `json:"route_binding_sha256"`
}

func (g Grant) Authorize(now time.Time, originalSource, originalSPKI, localTarget, localSPKI, account string, q Request) error {
	if err := g.ValidateCredit(); err != nil {
		return err
	}
	if now.IsZero() || !now.Before(g.Expires) || q.Version != 1 || q.TTL <= 0 || q.TTL > g.MaxTTL ||
		!hexID(q.ConnectionID, 16) || !hexID(q.RouteBindingHash, 32) ||
		originalSource != g.SourcePin || originalSPKI != g.SourceSPKI || localTarget != g.TargetPin || localSPKI != g.TargetSPKI || account != g.Account ||
		q.Workspace != g.Workspace || q.Generation != g.Generation || q.Source != g.Source || q.Target != g.Target ||
		q.SourcePin != g.SourcePin || q.TargetPin != g.TargetPin || q.Service != g.Endpoint.Name || q.GrantRevision != g.Revision {
		return errors.New("original source lacks current exact TCP service grant")
	}
	return nil
}

// Digest binds the installed endpoint and limits to the local review record.
func (g Grant) Digest() (string, error) {
	if err := g.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
