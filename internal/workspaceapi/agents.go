package workspaceapi

import (
	"github.com/inferal-oss/arm/internal/published"
	"github.com/inferal-oss/arm/internal/routing"
	"time"
)

type AgentPublicationCommit struct {
	Request published.Request `json:"request"`
	Expect  string            `json:"expect_sha256"`
}
type AgentPublicationWithdraw struct {
	Workspace string               `json:"workspace"`
	Endpoint  published.Address    `json:"endpoint"`
	Request   published.Withdrawal `json:"request"`
}
type AgentPublicationPage struct {
	Workspace string `json:"workspace"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
}
type AgentPublicationPageResult struct {
	Workspace    string                  `json:"workspace"`
	Publications []published.Publication `json:"publications"`
	Offset       int                     `json:"offset"`
	Next         int                     `json:"next"`
	Total        int                     `json:"total"`
}
type AgentEndpoints struct {
	Host  string          `json:"host"`
	Query published.Query `json:"query"`
}
type AgentMessage struct {
	Workspace    string           `json:"workspace"`
	ID           string           `json:"id"`
	Session      string           `json:"session"`
	Registration string           `json:"registration"`
	Key          string           `json:"key"`
	Target       string           `json:"target_host,omitempty"`
	To           routing.Endpoint `json:"target_endpoint"`
	Message      string           `json:"message_id,omitempty"`
	Body         string           `json:"body"`
	TTL          time.Duration    `json:"ttl_ns"`
	Reconcile    bool             `json:"reconcile,omitempty"`
}
