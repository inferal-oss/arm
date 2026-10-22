package database

import "time"

// Independent exact routed-ledger mirrors. Producer shape changes require a
// separate destructive-proof review; these are deliberately not type aliases.
type proofRouteEndpoint struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Generation string `json:"generation"`
}
type proofRouteReference struct {
	ID     string             `json:"message_id"`
	Digest string             `json:"message_sha256"`
	Origin string             `json:"origin_host"`
	From   proofRouteEndpoint `json:"origin_endpoint"`
	Target string             `json:"target_host"`
	To     proofRouteEndpoint `json:"target_endpoint"`
}
type proofRouteEnvelope struct {
	OriginPin        string               `json:"origin_pin"`
	TargetPin        string               `json:"target_pin"`
	Schema           int                  `json:"schema_version"`
	Workspace        string               `json:"workspace"`
	Generation       string               `json:"generation"`
	ID               string               `json:"message_id"`
	Key              string               `json:"client_key"`
	Origin           string               `json:"origin_host"`
	From             proofRouteEndpoint   `json:"origin_endpoint"`
	Target           string               `json:"target_host"`
	To               proofRouteEndpoint   `json:"target_endpoint"`
	OriginMembership int64                `json:"origin_membership"`
	TargetMembership int64                `json:"target_membership"`
	Body             string               `json:"body"`
	Created          time.Time            `json:"created_at"`
	Expires          time.Time            `json:"expires_at"`
	ReplyTo          *proofRouteReference `json:"reply_to,omitempty"`
}
type proofRouteIntent struct {
	Workspace string               `json:"workspace"`
	Key       string               `json:"key"`
	Target    string               `json:"target_host"`
	From      proofRouteEndpoint   `json:"origin_endpoint"`
	To        proofRouteEndpoint   `json:"target_endpoint"`
	Body      string               `json:"body"`
	TTL       time.Duration        `json:"ttl_ns"`
	ReplyTo   *proofRouteReference `json:"reply_to,omitempty"`
}
type proofRouteReceipt struct {
	Schema     int                `json:"schema_version"`
	Workspace  string             `json:"workspace"`
	Generation string             `json:"generation"`
	ID         string             `json:"message_id"`
	Digest     string             `json:"message_sha256"`
	Origin     string             `json:"origin_host"`
	From       proofRouteEndpoint `json:"origin_endpoint"`
	Target     string             `json:"target_host"`
	To         proofRouteEndpoint `json:"target_endpoint"`
	State      string             `json:"state"`
	Revision   int64              `json:"revision"`
	At         time.Time          `json:"recorded_at"`
	Code       string             `json:"code,omitempty"`
}
type proofRouteOutbox struct {
	Schema     int                `json:"schema_version"`
	Workspace  string             `json:"workspace"`
	Authority  proofWSAuthority   `json:"local_authority"`
	Intent     proofRouteIntent   `json:"intent"`
	IntentHash string             `json:"intent_sha256"`
	Envelope   proofRouteEnvelope `json:"envelope"`
	Phase      string             `json:"phase"`
	Revision   int64              `json:"revision"`
	Created    time.Time          `json:"created_at"`
	Updated    time.Time          `json:"updated_at"`
	Receipt    *proofRouteReceipt `json:"receipt,omitempty"`
}
type proofRouteInbox struct {
	Schema    int                `json:"schema_version"`
	Workspace string             `json:"workspace"`
	Envelope  proofRouteEnvelope `json:"envelope"`
	Receipt   proofRouteReceipt  `json:"receipt"`
	Received  time.Time          `json:"received_at"`
}
type proofRouteRoot struct {
	Schema     int              `json:"schema_version"`
	Workspace  string           `json:"workspace"`
	Generation string           `json:"generation"`
	Authority  proofWSAuthority `json:"local_authority"`
	LocalPin   string           `json:"local_pin"`
	State      string           `json:"state"`
	Revision   int64            `json:"revision"`
	Created    time.Time        `json:"created_at"`
	Updated    time.Time        `json:"updated_at"`
}
type proofRouteChange struct {
	Schema      int             `json:"schema_version"`
	Workspace   string          `json:"workspace"`
	Kind        string          `json:"kind"`
	Actor       string          `json:"actor"`
	Key         string          `json:"key"`
	Message     string          `json:"message_id"`
	Digest      string          `json:"message_sha256"`
	Phase       string          `json:"phase"`
	ReceiptHash string          `json:"receipt_sha256,omitempty"`
	Before      *proofRouteRoot `json:"before,omitempty"`
	After       proofRouteRoot  `json:"after"`
}
