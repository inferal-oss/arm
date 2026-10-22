package database

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/meshssh"
)

// These mirrors replace producer map/any previews at the destructive boundary.
// They are exact closed JSON shapes: opaque RawMessage is decoded again before
// it can provide settlement, never merely hashed or unmarshalled permissively.
type proofIdentityPreview struct {
	Host        string    `json:"host"`
	Name        string    `json:"name"`
	Owner       string    `json:"owner_uid"`
	Username    string    `json:"owner_username"`
	Address     string    `json:"address"`
	Fingerprint string    `json:"certificate_sha256"`
	SPKI        string    `json:"public_key_sha256"`
	Expires     time.Time `json:"certificate_expires"`
}
type proofPriorTrust struct {
	Identity proofIdentityPreview `json:"identity"`
	Pair     string               `json:"pair"`
	Revision int                  `json:"revision"`
	State    string               `json:"state"`
	Grant    mesh.Grant           `json:"grant"`
}
type proofNodePreview struct {
	Running        bool                  `json:"running"`
	Host           string                `json:"host"`
	Home           string                `json:"home"`
	OS             string                `json:"os"`
	Arch           string                `json:"arch"`
	Version        string                `json:"version"`
	Owner          local.Identity        `json:"owner"`
	Startup        mesh.StartupPolicy    `json:"configured_startup"`
	StartupSource  string                `json:"startup_provenance"`
	Route          *mesh.SSHRoute        `json:"ssh_route"`
	PrivateIPs     []string              `json:"assigned_private_interfaces"`
	Identity       *proofIdentityPreview `json:"identity,omitempty"`
	IdentityStatus string                `json:"identity_status,omitempty"`
	Prior          *proofPriorTrust      `json:"prior_trust,omitempty"`
}
type proofPreparedPreview struct {
	Identity proofIdentityPreview `json:"identity"`
	Listen   string               `json:"listen"`
	Expires  time.Time            `json:"reservation_expires"`
	Existing bool                 `json:"existing_listener"`
}
type proofCoordConnectReview struct {
	Operation      string                 `json:"operation"`
	Key            string                 `json:"key"`
	Request        meshops.ConnectRequest `json:"request"`
	SSH            meshssh.Identity       `json:"ssh_identity"`
	Local          proofNodePreview       `json:"local"`
	Remote         proofNodePreview       `json:"remote"`
	LocalPrepared  *proofPreparedPreview  `json:"local_prepared,omitempty"`
	RemotePrepared *proofPreparedPreview  `json:"remote_prepared,omitempty"`
	Warning        string                 `json:"warning"`
}
type proofCoordDisconnectReview struct {
	Operation      string        `json:"operation"`
	Key            string        `json:"key"`
	Host           mesh.Identity `json:"host"`
	Pair           string        `json:"pair"`
	LocalRevision  int           `json:"local_revision"`
	RemoteRevision int           `json:"remote_revision"`
	SSHAccount     string        `json:"ssh_account"`
	SSHHostKey     string        `json:"ssh_host_key"`
	Effect         string        `json:"effect"`
	Reason         string        `json:"reason"`
}
type proofCoordRestoreReview struct {
	Operation string           `json:"operation"`
	Original  string           `json:"original_key"`
	Pair      string           `json:"pair"`
	Local     proofNodePreview `json:"local"`
	Remote    proofNodePreview `json:"remote"`
	Effect    string           `json:"effect"`
}
type proofCoordVerifyReview struct {
	Operation      string           `json:"operation"`
	Original       string           `json:"original_key"`
	Pair           string           `json:"pair"`
	LocalListen    string           `json:"local_listen"`
	RemoteListen   string           `json:"remote_listen"`
	LocalRevision  int              `json:"local_revision"`
	RemoteRevision int              `json:"remote_revision"`
	Forward        mesh.Grant       `json:"source_permissions_on_target"`
	Reverse        mesh.Grant       `json:"target_permissions_on_source"`
	Local          proofNodePreview `json:"local"`
	Remote         proofNodePreview `json:"remote"`
	Effect         string           `json:"effect"`
}
type proofCoordConnectResult struct {
	Host    string        `json:"connected"`
	Name    string        `json:"name"`
	Key     string        `json:"key"`
	Pair    string        `json:"pair"`
	Local   mesh.Identity `json:"local_identity"`
	Remote  mesh.Identity `json:"remote_identity"`
	Forward mesh.Grant    `json:"source_permissions_on_target"`
	Reverse mesh.Grant    `json:"target_permissions_on_source"`
	Reused  bool          `json:"reused,omitempty"`
}
type proofCoordDisconnectResult struct {
	Host    string `json:"disconnected"`
	Pending bool   `json:"remote_pending"`
	Key     string `json:"key"`
}
type proofCoordPendingResult struct {
	Host    string `json:"local_disconnected"`
	Pending bool   `json:"remote_pending"`
	Key     string `json:"key"`
	Detail  string `json:"detail"`
	Retry   string `json:"retry"`
}
type proofCoordRecord struct {
	provisionReview  *meshops.ProvisionPlan
	provisionResult  *meshops.ProvisionResult
	op               mesh.Coordination
	request          meshops.StartRequest
	reviewOperation  string
	connectReview    *proofCoordConnectReview
	restoreReview    *proofCoordRestoreReview
	verifyReview     *proofCoordVerifyReview
	disconnectReview *proofCoordDisconnectReview
	connectResult    *proofCoordConnectResult
	disconnectResult *proofCoordDisconnectResult
	canonicalRequest string
}

func compactProofJSON(raw json.RawMessage) (string, error) {
	// strictEvidence handles exact known object shapes before this comparison.
	// Compact preserves writer member order for immutable hash validation.
	var value bytes.Buffer
	if err := json.Compact(&value, raw); err != nil {
		return "", err
	}
	return value.String(), nil
}
