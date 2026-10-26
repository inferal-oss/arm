package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
)

type meshReviewIdentity struct {
	Host        string    `json:"host"`
	Name        string    `json:"name"`
	Owner       string    `json:"owner_uid"`
	Username    string    `json:"owner_username"`
	Address     string    `json:"address"`
	Certificate string    `json:"certificate_sha256"`
	PublicKey   string    `json:"public_key_sha256"`
	Expires     time.Time `json:"certificate_expires"`
}
type meshReviewNode struct {
	Home     string              `json:"home"`
	Version  string              `json:"version"`
	Identity *meshReviewIdentity `json:"identity"`
	Prior    *struct {
		Pair     string      `json:"pair"`
		Revision int         `json:"revision"`
		State    string      `json:"state"`
		Grant    *mesh.Grant `json:"grant"`
	} `json:"prior_trust"`
}
type meshReviewPrepared struct {
	Identity *meshReviewIdentity `json:"identity"`
	Listen   string              `json:"listen"`
	Expires  time.Time           `json:"reservation_expires"`
	Existing bool                `json:"existing_listener"`
}
type meshTrustReview struct {
	Operation      string                 `json:"operation"`
	Request        meshops.ConnectRequest `json:"request"`
	Local          meshReviewNode         `json:"local"`
	Remote         meshReviewNode         `json:"remote"`
	LocalPrepared  meshReviewPrepared     `json:"local_prepared"`
	RemotePrepared meshReviewPrepared     `json:"remote_prepared"`
	SSH            struct {
		Account string `json:"account"`
		HostKey string `json:"host_key"`
	} `json:"ssh_identity"`
	Host           mesh.Identity `json:"host"`
	Pair           string        `json:"pair"`
	LocalRevision  int           `json:"local_revision"`
	RemoteRevision int           `json:"remote_revision"`
	SSHAccount     string        `json:"ssh_account"`
	SSHKey         string        `json:"ssh_host_key"`
	Reason         string        `json:"reason"`
	Effect         string        `json:"effect"`
	Warning        string        `json:"warning"`
}

func grantView(g mesh.Grant) *model.NodeGrant {
	return &model.NodeGrant{Permissions: g.Permissions, Limits: g.Limits, MaxTTL: g.MaxTTL, Contacts: g.Contacts, OperatorInbox: g.OperatorInbox}
}
func meshIdentitySummary(label string, identity *meshReviewIdentity, prepared meshReviewPrepared, prior meshReviewNode) string {
	if identity == nil {
		return label + "\nIdentity unavailable\n"
	}
	account := safe(identity.Owner)
	if identity.Username != "" {
		account = safe(identity.Username) + " (UID " + account + ")"
	}
	text := fmt.Sprintf("%s\n%s\nHost: %s\nRun as: %s\nAdvertised endpoint: %s\nCertificate SHA256: %s\nPublic key SHA256: %s\nCertificate expires: %s\n", strong.Render(label), strong.Render(safe(identity.Name)), safe(identity.Host), account, safe(identity.Address), safe(identity.Certificate), safe(identity.PublicKey), identity.Expires.Format(time.RFC3339))
	if prepared.Listen != "" {
		text += fmt.Sprintf("Listener: %s · already listening: %t\nReservation expires: %s\n", safe(prepared.Listen), prepared.Existing, prepared.Expires.Format(time.RFC3339))
	}
	if prior.Prior != nil {
		text += fmt.Sprintf("Prior enrollment: %s · revision %d · %s\n", safe(prior.Prior.Pair), prior.Prior.Revision, safe(prior.Prior.State))
	} else if prepared.Listen != "" || prior.Identity != nil {
		text += "Prior enrollment: none reported\n"
	}
	return text
}

func meshReviewSummary(raw json.RawMessage) (string, bool) {
	var operation struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(raw, &operation) == nil && operation.Operation == "install-and-start-arm-node" {
		return meshProvisionReviewSummary(raw)
	}
	var schema struct {
		Schema string `json:"schema"`
	}
	if json.Unmarshal(raw, &schema) == nil && schema.Schema == mesh.UninstallSchema {
		return meshUninstallReviewSummary(raw)
	}
	var review meshTrustReview
	if json.Unmarshal(raw, &review) != nil {
		return "Review could not be decoded. Inspect the full record; approval is unavailable.", false
	}
	var out strings.Builder
	switch review.Operation {
	case "stop-arm-node":
		return meshStopReviewSummary(raw)
	case "connect-direct-peers", "reconnect-direct-peers":
		left, right := review.LocalPrepared.Identity, review.RemotePrepared.Identity
		if left == nil {
			left = review.Local.Identity
		}
		if right == nil {
			right = review.Remote.Identity
		}
		if left == nil || right == nil || left.Host == "" || right.Host == "" || left.Certificate == "" || right.Certificate == "" {
			return "Exact source/destination identities and pins are missing. Inspect the full record; approval is unavailable.", false
		}
		out.WriteString(meshIdentitySummary("THIS SOURCE NODE", left, review.LocalPrepared, review.Local) + "\n")
		out.WriteString(meshIdentitySummary("DESTINATION NODE", right, review.RemotePrepared, review.Remote) + "\n")
		fmt.Fprintf(&out, "VERIFIED SSH BOOTSTRAP\nAccount: %s\nHost key: %s\nDestination: %s\n\n", safe(review.SSH.Account), safe(review.SSH.HostKey), safe(review.Request.Destination))
		if review.Operation == "reconnect-direct-peers" {
			previous := review.Request.Reconnect
			if previous == nil {
				return "Exact previous reconnect binding missing; approval unavailable.", false
			}
			fmt.Fprintf(&out, "RECONNECT · NEW ENROLLMENT\nPrevious pair: %s · revision %d\nInstalled helper command: %s\nReported ARM home: %s\nReported ARM version: %s\nHistoric executable byte equality is not established; no install or upgrade occurs.\n\n", safe(previous.Binding.Prior.Pair), previous.Binding.Prior.Revision, safe(review.Request.Binary), safe(review.Remote.Home), safe(review.Remote.Version))
		}
		out.WriteString("WE MAY DO THERE · source → destination\n" + meshGrantText(grantView(review.Request.Forward)) + "\n\n")
		out.WriteString("PEER MAY DO HERE · destination → source\n" + meshGrantText(grantView(review.Request.Reverse)) + "\n\n")
		fmt.Fprintf(&out, "Replace revoked enrollment: %t\nWhy: %s\n\nCONSEQUENCES\n%s\n", review.Request.ReplaceRevoked, safe(review.Request.Reason), safe(review.Warning))
		if review.Request.Forward.Has("run") || review.Request.Reverse.Has("run") {
			out.WriteString("\nRUN executes as the destination OS account, including its file and credential access. It is not a sandbox.\n")
		}
	case "disconnect-direct-peer":
		h := review.Host
		if h.Host == "" || review.Pair == "" {
			return "Exact disconnect identity/enrollment missing. Inspect the full record; approval is unavailable.", false
		}
		identity := &meshReviewIdentity{Host: h.Host, Name: h.Name, Owner: h.Owner, Username: h.OwnerUsername, Address: h.Address, Certificate: h.Fingerprint, PublicKey: h.SPKI, Expires: h.Expires}
		out.WriteString(meshIdentitySummary("DISCONNECT THIS EXACT PEER", identity, meshReviewPrepared{}, meshReviewNode{}))
		fmt.Fprintf(&out, "\nPair: %s\nLocal enrollment revision: %d\nDestination enrollment revision: %d\nVerified SSH account: %s\nVerified SSH key: %s\nWhy: %s\n\nCONSEQUENCES\n%s\n", safe(review.Pair), review.LocalRevision, review.RemoteRevision, safe(review.SSHAccount), safe(review.SSHKey), safe(review.Reason), safe(review.Effect))
	case "restore-existing-connection", "verify-existing-connection":
		text, known := meshRecoveryReviewSummary(raw)
		if !known {
			return text, false
		}
		out.WriteString(text)
	default:
		return "Unrecognized review type. Inspect the full record; approval is unavailable.", false
	}
	out.WriteString("\n[i] Full immutable review in the collapsible operation record. Default: keep unchanged; nothing is sent until explicit confirmation.")
	return out.String(), true
}

func meshDraftSummary(raw json.RawMessage) string {
	var q meshPrepareRequest
	if json.Unmarshal(raw, &q) != nil {
		return "Draft unavailable"
	}
	if q.Reconnect != nil {
		d := q.Reconnect
		locator := d.Destination
		if d.Connector != "" {
			locator = "Reviewed connector @" + d.Connector
		}
		if locator == "" {
			locator = "Retained locator, verified anew"
		}
		return "Exact revoked host: " + safe(d.Host) + "\nSSH locator: " + safe(locator) + "\nWhy: " + safe(d.Reason) + "\n\nNEW PERMISSIONS THERE\n" + meshGrantText(grantView(d.Forward)) + "\n\nNEW PERMISSIONS HERE\n" + meshGrantText(grantView(d.Reverse)) + "\nPrevious grants and pending disconnect receipts remain context, not restored authority.\nExact preparation key: " + safe(q.Key)
	}
	if q.Connect != nil {
		d := q.Connect
		destination := d.Destination
		if d.Connector != "" {
			destination = "Reviewed connector @" + d.Connector
		}
		return "Destination: " + safe(destination) + "\nWhy: " + safe(d.Reason) + "\n\nWE MAY DO THERE\n" + meshGrantText(grantView(d.Forward)) + "\n\nPEER MAY DO HERE\n" + meshGrantText(grantView(d.Reverse)) + fmt.Sprintf("\n\nReplace revoked enrollment: %t\nExact preparation key: %s", d.ReplaceRevoked, q.Key)
	}
	if q.Disconnect != nil {
		return "Exact peer: " + safe(q.Disconnect.Host) + "\nPair: " + safe(q.Disconnect.Pair) + "\nWhy: " + safe(q.Disconnect.Reason) + "\nExact preparation key: " + safe(q.Key)
	}
	return "Draft unavailable"
}
