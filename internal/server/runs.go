package server

import (
	"bytes"
	"encoding/json"
	"io"

	"getarm.org/internal/model"
)

func (s *Server) runOperation(operation string, params json.RawMessage) (any, error) {
	decode := func(v any) error {
		decoder := json.NewDecoder(bytes.NewReader(params))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(v); err != nil {
			return model.Err("invalid_request", err.Error())
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return model.Err("invalid_request", "run parameters must be one JSON object")
		}
		return nil
	}
	switch operation {
	case "runs":
		return s.Manager.Runs()
	case "run_create":
		var q model.RunCreateRequest
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.CreateRun(q)
	case "run_append":
		var q model.RunAppendRequest
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.AppendRun(q)
	case "run_finish":
		var q model.RunFinishRequest
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.FinishRun(q)
	case "run_inspect", "run_output":
		var q struct {
			ID     string `json:"id"`
			Offset int64  `json:"offset"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		if operation == "run_inspect" {
			return s.Manager.InspectRun(q.ID)
		}
		return s.Manager.RunOutput(q.ID, q.Offset)
	}
	return nil, model.Err("invalid_request", "unknown run operation")
}
