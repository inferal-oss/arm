package server

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/inferal-oss/arm/internal/model"
)

func (s *Server) leaseReviewOperation(op string, raw json.RawMessage) (any, error) {
	decode := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			return model.Err("invalid_request", err.Error())
		}
		if err := d.Decode(new(any)); err != io.EOF {
			return model.Err("invalid_request", "expected exactly one request object")
		}
		return nil
	}
	switch op {
	case "lease_review_list":
		var q model.LeaseReviewQuery
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.LeaseReviews(q)
	case "lease_review_inspect":
		var q struct {
			ID string `json:"id"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.InspectLeaseReview(q.ID)
	case "lease_review_policy":
		var q struct {
			Lease string `json:"lease"`
		}
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.LeaseReviewPolicy(q.Lease)
	case "lease_review_configure":
		var q model.LeaseReviewConfigure
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.ConfigureLeaseReview(q)
	case "lease_review_reply":
		var q model.LeaseReviewReply
		if err := decode(&q); err != nil {
			return nil, err
		}
		return s.Manager.ReplyLeaseReview(q)
	}
	return nil, model.Err("invalid_request", "unknown lease review operation")
}
