package workspaceapi

import (
	"github.com/inferal-oss/arm/internal/routing"
	"time"
)

type MessageSend struct {
	Workspace string        `json:"workspace"`
	Key       string        `json:"key"`
	Target    string        `json:"target_host"`
	Body      string        `json:"body"`
	TTL       time.Duration `json:"ttl_ns"`
}
type MessageReply struct {
	Workspace string        `json:"workspace"`
	Key       string        `json:"key"`
	Message   string        `json:"message_id"`
	Body      string        `json:"body"`
	TTL       time.Duration `json:"ttl_ns"`
}
type MessageLookup struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Reconcile bool   `json:"reconcile"`
}
type MessagePage struct {
	Workspace string `json:"workspace"`
	Side      string `json:"side"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
	Revision  int64  `json:"revision"`
}
type MessageResult struct {
	Workspace string           `json:"workspace"`
	Key       string           `json:"key"`
	Message   string           `json:"message_id"`
	Digest    string           `json:"message_sha256"`
	Origin    string           `json:"origin_host"`
	From      routing.Endpoint `json:"origin_endpoint"`
	Target    string           `json:"target_host"`
	To        routing.Endpoint `json:"target_endpoint"`
	Phase     string           `json:"phase"`
	Revision  int64            `json:"revision"`
	Receipt   *routing.Receipt `json:"receipt,omitempty"`
}

func RoutingResult(o routing.Outbox) MessageResult {
	return MessageResult{Workspace: o.Workspace, Key: o.Intent.Key, Message: o.Envelope.ID, Digest: o.Envelope.Digest(), Origin: o.Envelope.Origin, From: o.Envelope.From, Target: o.Envelope.Target, To: o.Envelope.To, Phase: o.Phase, Revision: o.Revision, Receipt: o.Receipt}
}

type MessagePageResult struct {
	Workspace   string           `json:"workspace"`
	Revision    int64            `json:"revision"`
	Initialized bool             `json:"initialized"`
	Root        *routing.Root    `json:"root,omitempty"`
	Outbox      []routing.Outbox `json:"outbox,omitempty"`
	Inbox       []routing.Inbox  `json:"inbox,omitempty"`
	Offset      int              `json:"offset"`
	Next        int              `json:"next"`
	Total       int              `json:"total"`
}

type RevisionRequest struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Revision  int64  `json:"revision"`
}
