package server

import (
	"bytes"
	"encoding/json"
	"io"

	"getarm.org/internal/model"
	"getarm.org/internal/observe"
)

func (s *Server) nodeMetadata() (model.NodeMetadata, error) {
	status, err := s.Manager.Status()
	if err != nil {
		return model.NodeMetadata{}, err
	}
	return observe.NodeMetadata(s.withServerCapabilities(status)), nil
}

// No target selectors, annotations or forwarded operations are accepted by this
// local read. Remote routing stays in the separately authorized mesh gateway.
func nodeMetadataParams(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] != '{' {
		return model.Err("invalid_request", "node metadata accepts only an empty parameter object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var q struct{}
	if err := decoder.Decode(&q); err != nil {
		return model.Err("invalid_request", "node metadata accepts no selectors or mutation fields")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return model.Err("invalid_request", "node metadata parameters contain trailing data")
	}
	return nil
}

// Feature inventory is shared by status and metadata. These software declarations
// do not confer grants or authenticate a peer; destination policy still applies.
func (s *Server) withServerCapabilities(status model.Status) model.Status {
	status.Capabilities = append(status.Capabilities, "pool-journal-pages", "automatic-mesh-lifecycle", "database-inspection-backup", "reported-node-metadata")
	if s.authority != "" {
		status.Capabilities = append(status.Capabilities, "browser-dashboard-observation", "browser-reviewed-lease-controls", "browser-reviewed-messaging", "browser-reviewed-pool-controls")
	}
	if s.monitor != nil {
		status.Capabilities = append(status.Capabilities, "host-monitor-stream")
	}
	return status
}
