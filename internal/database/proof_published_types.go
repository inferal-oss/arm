package database

import (
	"github.com/yrashk/arm/internal/agents"
	"time"
)

// Closed independent JSON mirrors, never aliases to writer records.
type proofPublishedAddress struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Generation string `json:"generation"`
}
type proofPublishedRequest struct {
	Workspace    string `json:"workspace"`
	ID           string `json:"id"`
	Session      string `json:"session"`
	Registration string `json:"registration"`
	Label        string `json:"label"`
	Delivery     string `json:"delivery"`
	Key          string `json:"key"`
	Reason       string `json:"reason"`
}
type proofPublishedWithdrawal struct {
	Key      string `json:"key"`
	Reason   string `json:"reason"`
	Revision int64  `json:"revision"`
}
type proofPublishedScope struct {
	Workspace         string           `json:"workspace"`
	Generation        string           `json:"generation"`
	Authority         proofWSAuthority `json:"authority"`
	Pin               string           `json:"pin"`
	Membership        int64            `json:"membership"`
	WorkspaceRevision int64            `json:"workspace_revision"`
}
type proofPublishedPlan struct {
	Request    proofPublishedRequest `json:"request"`
	Scope      proofPublishedScope   `json:"scope"`
	ContactSHA string                `json:"contact_sha256"`
	ReviewSHA  string                `json:"review_sha256"`
}
type proofPublishedActor struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
}
type proofPublishedPublication struct {
	Schema     int                       `json:"schema_version"`
	Scope      proofPublishedScope       `json:"scope"`
	Request    proofPublishedRequest     `json:"request"`
	Endpoint   proofPublishedAddress     `json:"endpoint"`
	Contact    agents.Contact            `json:"contact"`
	ContactSHA string                    `json:"contact_sha256"`
	ReviewSHA  string                    `json:"review_sha256"`
	Actor      proofPublishedActor       `json:"actor"`
	State      string                    `json:"state"`
	Revision   int64                     `json:"revision"`
	Created    time.Time                 `json:"created_at"`
	Updated    time.Time                 `json:"updated_at"`
	Withdrawal *proofPublishedWithdrawal `json:"withdrawal,omitempty"`
}
type proofPublishedChange struct {
	Workspace string                     `json:"workspace"`
	Sequence  int64                      `json:"sequence"`
	Before    *proofPublishedPublication `json:"before,omitempty"`
	After     proofPublishedPublication  `json:"after"`
}
type proofPublishedContext struct {
	Workspace  string                `json:"workspace"`
	Generation string                `json:"generation"`
	Message    string                `json:"message_id"`
	Digest     string                `json:"message_sha256"`
	Origin     string                `json:"origin_host"`
	Target     string                `json:"target_host"`
	From       proofPublishedAddress `json:"origin_endpoint"`
	To         proofPublishedAddress `json:"target_endpoint"`
}
type proofPublishedLink struct {
	Schema       int                   `json:"schema_version"`
	Context      proofPublishedContext `json:"context"`
	LocalMessage string                `json:"local_message"`
	Recipient    string                `json:"recipient"`
	Registration string                `json:"registration"`
	ContactSHA   string                `json:"contact_sha256"`
	Created      time.Time             `json:"created_at"`
}
