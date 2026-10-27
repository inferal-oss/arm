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
	return ConnectEndpoint(ctx, ep)
}

// ConnectEndpoint connects to an already read endpoint without another filesystem
// read. Callers that need bounded metadata reads can validate the file first.
func ConnectEndpoint(ctx context.Context, ep local.Endpoint) (*websocket.Conn, error) {
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
	return call(ctx, c, operation, params, result)
}

// CallEndpoint uses the normal authenticated request protocol with an existing
// endpoint snapshot. It neither reads nor creates ARM state.
func CallEndpoint(ctx context.Context, endpoint local.Endpoint, operation string, params, result any) error {
	c, e := ConnectEndpoint(ctx, endpoint)
	if e != nil {
		return e
	}
	return call(ctx, c, operation, params, result)
}

func CallWithDiagnostics(ctx context.Context, home, operation string, params, result any) (*model.Error, error) {
	c, err := Connect(ctx, home)
	if err != nil {
		return nil, err
	}
	return callWithDiagnostics(ctx, c, operation, params, result)
}

func CallEndpointWithDiagnostics(ctx context.Context, endpoint local.Endpoint, operation string, params, result any) (*model.Error, error) {
	c, err := ConnectEndpoint(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return callWithDiagnostics(ctx, c, operation, params, result)
}

func call(ctx context.Context, c *websocket.Conn, operation string, params, result any) error {
	_, err := callWithDiagnostics(ctx, c, operation, params, result)
	return err
}

func callWithDiagnostics(ctx context.Context, c *websocket.Conn, operation string, params, result any) (*model.Error, error) {
	defer c.CloseNow()
	raw, e := json.Marshal(params)
	if e != nil {
		return nil, e
	}
	id := local.ID()
	if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: id, Operation: operation, Params: raw}); e != nil {
		return nil, e
	}
	var resp struct {
		Version          int             `json:"version"`
		NamingDiagnostic *model.Error    `json:"naming_diagnostic"`
		ID               string          `json:"id"`
		Result           json.RawMessage `json:"result"`
		Error            *model.Error    `json:"error"`
	}
	if e = wsjson.Read(ctx, c, &resp); e != nil {
		return nil, e
	}
	if resp.ID != id {
		return nil, fmt.Errorf("response request ID mismatch")
	}
	if resp.NamingDiagnostic != nil && resp.Version != 1 {
		return nil, fmt.Errorf("naming diagnostic has unsupported protocol version")
	}
	diagnostic := namingDiagnostic(resp.NamingDiagnostic)
	if resp.Error != nil {
		return diagnostic, resp.Error
	}
	if result != nil {
		decoder := json.NewDecoder(bytes.NewReader(resp.Result))
		decoder.UseNumber()
		return diagnostic, decoder.Decode(result)
	}
	return diagnostic, nil
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
