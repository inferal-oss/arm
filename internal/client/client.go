package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func Connect(ctx context.Context, home string) (*websocket.Conn, error) {
	ep, e := local.ReadEndpoint(home)
	if e != nil {
		return nil, fmt.Errorf("backend unavailable; run arm daemon start: %w", e)
	}
	c, _, e := websocket.Dial(ctx, ep.URL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + ep.Token}}})
	if e != nil {
		return nil, fmt.Errorf("backend unavailable; run arm daemon start: %w", e)
	}
	c.SetReadLimit(4 << 20)
	return c, nil
}
func Call(ctx context.Context, home, operation string, params, result any) error {
	c, e := Connect(ctx, home)
	if e != nil {
		return e
	}
	defer c.CloseNow()
	raw, e := json.Marshal(params)
	if e != nil {
		return e
	}
	id := local.ID()
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: id, Operation: operation, Params: raw}); e != nil {
		return e
	}
	var resp struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *model.Error    `json:"error"`
	}
	if e = wsjson.Read(ctx, c, &resp); e != nil {
		return e
	}
	if resp.ID != id {
		return fmt.Errorf("response request ID mismatch")
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil {
		decoder := json.NewDecoder(bytes.NewReader(resp.Result))
		decoder.UseNumber()
		return decoder.Decode(result)
	}
	return nil
}

func Follow(ctx context.Context, home string, after int64, handle func(model.Event) error) error {
	c, e := Connect(ctx, home)
	if e != nil {
		return e
	}
	defer c.CloseNow()
	raw, _ := json.Marshal(map[string]int64{"after": after})
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: local.ID(), Operation: "subscribe", Params: raw}); e != nil {
		return e
	}
	for {
		var response struct {
			Event *model.Event `json:"event"`
			Error *model.Error `json:"error"`
		}
		if e = wsjson.Read(ctx, c, &response); e != nil {
			return e
		}
		if response.Error != nil {
			return response.Error
		}
		if response.Event != nil {
			if e = handle(*response.Event); e != nil {
				return e
			}
		}
	}
}
