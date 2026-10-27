package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

// WatchMonitor delivers the atomic initial snapshot and subsequent bounded
// pages on the same request/response WebSocket protocol as other clients.
func WatchMonitor(ctx context.Context, home string, q model.MonitorQuery, handle func(model.MonitorPage) error) error {
	c, err := Connect(ctx, home)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	id := local.ID()
	if err = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: id, Operation: "monitor_subscribe", Params: raw}); err != nil {
		return err
	}
	initial := true
	for {
		var response struct {
			ID           string             `json:"id"`
			Subscription string             `json:"subscription"`
			Result       *model.MonitorPage `json:"result"`
			Telemetry    *model.MonitorPage `json:"telemetry"`
			Error        *model.Error       `json:"error"`
		}
		if err = wsjson.Read(ctx, c, &response); err != nil {
			return err
		}
		if response.Error != nil {
			return response.Error
		}
		page := response.Telemetry
		if initial {
			if response.ID != id || response.Result == nil {
				return fmt.Errorf("monitor initial response mismatch")
			}
			page = response.Result
			initial = false
		}
		if response.Telemetry != nil && response.Subscription != id {
			return fmt.Errorf("monitor subscription identity mismatch")
		}
		if page == nil {
			return fmt.Errorf("monitor stream did not contain a sample page")
		}
		if err = handle(*page); err != nil {
			return err
		}
	}
}
