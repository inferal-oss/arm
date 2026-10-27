package client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

// OpenNodeSession creates a dedicated immutable selected-node connection through
// the private local gateway. It never retries, changes a selected authority, or
// forwards the local bearer credential to the destination.
func OpenNodeSession(ctx context.Context, home string, selection model.NodeSelection) (*Session, model.NodeContextReport, error) {
	var report model.NodeContextReport
	if !nodeHex(selection.Host, 16) || !nodeHex(selection.Source, 16) {
		return nil, report, model.Err("invalid_request", "exact source and destination host IDs are required")
	}
	ep, err := local.ReadEndpoint(home)
	if err != nil {
		return nil, report, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, sessionConnectTimeout)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, ep.URL, &websocket.DialOptions{HTTPHeader: http.Header{
		"Authorization": {"Bearer " + ep.Token}, "Arm-Node-Context": {"v1"},
	}})
	if err != nil {
		return nil, report, fmt.Errorf("selected node connection unavailable; no local fallback: %w", err)
	}
	c.SetReadLimit(4 << 20)
	ok := false
	defer func() {
		if !ok {
			_ = c.CloseNow()
		}
	}()
	raw, err := json.Marshal(selection)
	if err != nil {
		return nil, report, err
	}
	id := local.ID()
	if err = wsjson.Write(dialCtx, c, model.WireRequest{Version: 1, ID: id, Operation: "node_context_open", Params: raw}); err != nil {
		return nil, report, err
	}
	var response struct {
		Version int                     `json:"version"`
		ID      string                  `json:"id"`
		Result  model.NodeContextReport `json:"result"`
		Error   *model.Error            `json:"error"`
	}
	if err = wsjson.Read(dialCtx, c, &response); err != nil {
		return nil, report, err
	}
	if response.Version != 1 || response.ID != id {
		return nil, report, fmt.Errorf("invalid selected-node negotiation response")
	}
	if response.Error != nil {
		return nil, report, response.Error
	}
	report = response.Result
	if err = validateNodeReport(selection, report); err != nil {
		return nil, model.NodeContextReport{}, err
	}
	ok = true
	return newSession(ctx, c), report, nil
}

func nodeHex(value string, bytes int) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == bytes && value == strings.ToLower(value)
}

func validateNodeReport(selection model.NodeSelection, report model.NodeContextReport) error {
	b := report.Context
	if b.Host != selection.Host || b.Source != selection.Source || b.Local != (b.Host == b.Source) {
		return model.Err("wrong_host", "node response differs from exact selected source/destination")
	}
	if !b.Local {
		scope := report.AccessScope
		if !report.ReadOnly || !nodeHex(b.Pair, 32) || !nodeHex(b.Pin, 32) || b.LocalRevision <= 0 || b.RemoteRevision <= 0 || scope == nil || scope.Source != b.Source || scope.Target != b.Host || scope.Pair != b.Pair || scope.Revision != b.RemoteRevision {
			return model.Err("node_context_changed", "remote node context lacks exact enrollment and current destination scope")
		}
	}
	return nil
}
