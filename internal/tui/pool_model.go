package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

// Leave room for the wire envelope within the backend’s 64 KiB frame limit.
const maxPoolRequestBytes = 60 << 10

type poolDraft struct{ operation, name, scope, unit, description, value, reason, id string }
type poolPlan struct {
	request                                model.PoolMutation
	before                                 *model.PoolView
	capacity, reserved, available, deficit int64
	state, name, scope, unit               string
}

func parsePoolInteger(text string, signed bool) (int64, error) {
	text = strings.TrimSpace(text)
	if len(text) == 0 || len(text) > 20 {
		return 0, fmt.Errorf("enter a whole int64 count")
	}
	digits := text
	if digits[0] == '+' || digits[0] == '-' {
		digits = digits[1:]
	}
	if digits == "" || strings.ContainsAny(digits, ".eE ") {
		return 0, fmt.Errorf("enter a whole base-10 count")
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("enter a whole base-10 count")
		}
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || (!signed && n < 0) {
		return 0, fmt.Errorf("count must fit int64; capacity cannot be negative")
	}
	return n, nil
}
func copyPoolMutation(request model.PoolMutation) model.PoolMutation {
	raw, _ := json.Marshal(request)
	var result model.PoolMutation
	json.Unmarshal(raw, &result)
	return result
}
func preparePoolPlan(draft poolDraft, view *model.PoolView, authority string) (*poolPlan, error) {
	if authority == "" || len(authority) > 256 || !utf8.ValidString(authority) {
		return nil, fmt.Errorf("refresh the local authority before reviewing a change")
	}
	if strings.TrimSpace(draft.reason) == "" || len(draft.reason) > 4096 || !utf8.ValidString(draft.reason) {
		return nil, fmt.Errorf("reason is required and must be at most 4096 bytes")
	}
	p := &poolPlan{name: draft.name, scope: draft.scope, unit: draft.unit, state: "active"}
	r := model.PoolMutation{Operation: draft.operation, Reason: draft.reason, Key: local.ID()}
	if draft.operation == "create" {
		r.Name = strings.TrimSpace(draft.name)
		r.Unit = strings.TrimSpace(draft.unit)
		if r.Unit == "" {
			r.Unit = "count"
		}
		r.Scope = draft.scope
		r.Description = draft.description
		if r.Name == "" || len(r.Name) > 128 || len(r.Unit) > 64 || len(r.Description) > 4096 || !poolPrintable(r.Name) || !poolPrintable(r.Unit) || !poolPrintable(r.Description) || (r.Scope != "host" && r.Scope != "global") {
			return nil, fmt.Errorf("enter a bounded name/unit/description and explicit host or global scope")
		}
		capacity, err := parsePoolInteger(draft.value, false)
		if err != nil {
			return nil, err
		}
		r.Capacity = &capacity
		p.capacity = capacity
		if r.Scope == "host" {
			r.Host = authority
		}
		r.Reviewed = &model.PoolReview{Authority: authority, Consumers: []model.LeaseRevision{}}
		p.name, p.unit = r.Name, r.Unit
	} else {
		if view == nil || view.Review == nil || view.Pool.Authority != authority || view.Review.Authority != authority || view.Review.Consumers == nil {
			return nil, fmt.Errorf("refresh the complete pool snapshot from this authority")
		}
		if len(view.Review.Consumers) > 8192 {
			return nil, fmt.Errorf("complete review exceeds 8192 consumers; scope was not truncated")
		}
		if view.Pool.Revision <= 0 || view.Pool.Revision == math.MaxInt64 || view.Pool.Capacity < 0 || view.Reserved < 0 {
			return nil, fmt.Errorf("refresh a valid non-exhausted pool definition and budget")
		}
		seen := map[string]bool{}
		for _, c := range view.Review.Consumers {
			if c.ID == "" || len(c.ID) > 128 || c.Revision <= 0 || seen[c.ID] {
				return nil, fmt.Errorf("refresh the complete valid consumer revision set")
			}
			seen[c.ID] = true
		}
		if view.Pool.State == "deleted" {
			return nil, fmt.Errorf("this is a retained deleted definition")
		}
		raw, _ := json.Marshal(view)
		var before model.PoolView
		json.Unmarshal(raw, &before)
		p.before = &before
		p.name, p.scope, p.unit, p.state = view.Pool.Name, view.Pool.Scope, view.Pool.Unit, view.Pool.State
		p.capacity, p.reserved = view.Pool.Capacity, view.Reserved
		r.ID = view.Pool.ID
		revision := view.Pool.Revision
		r.Revision = &revision
		r.Reviewed = view.Review
		switch draft.operation {
		case "set":
			capacity, err := parsePoolInteger(draft.value, false)
			if err != nil {
				return nil, err
			}
			r.Capacity = &capacity
			p.capacity = capacity
		case "adjust":
			delta, err := parsePoolInteger(draft.value, true)
			if err != nil || delta == 0 {
				return nil, fmt.Errorf("adjustment must be a nonzero signed int64 count")
			}
			if delta < -p.capacity || delta > math.MaxInt64-p.capacity {
				return nil, fmt.Errorf("adjustment would make capacity negative or overflow int64")
			}
			r.Delta = &delta
			p.capacity += delta
		case "drain":
			p.state = "draining"
		case "resume":
			p.state = "active"
		case "delete":
			p.state = "deleted"
		default:
			return nil, fmt.Errorf("unsupported pool operation")
		}
	}
	p.available = max(0, p.capacity-p.reserved)
	if p.state == "deleted" {
		p.available = 0
	}
	p.deficit = max(0, p.reserved-p.capacity)
	p.request = copyPoolMutation(r)
	raw, _ := json.Marshal(p.request)
	if len(raw) > maxPoolRequestBytes {
		return nil, fmt.Errorf("complete reviewed request exceeds the 60 KiB protocol budget; nothing was truncated or sent; use the native CLI for this larger scope")
	}
	return p, nil
}
func matchingPoolChange(r model.PoolMutation, c model.PoolChange) bool {
	if r.Reviewed == nil || c.Key != r.Key || c.Operation != r.Operation || c.Reason != r.Reason || c.Pool.Authority != r.Reviewed.Authority || c.Pool.ID == "" || c.Sequence <= 0 {
		return false
	}
	if r.Operation == "create" {
		return c.Pool.Name == r.Name && c.Pool.Scope == r.Scope && c.Pool.Host == r.Host && c.Pool.Unit == r.Unit && r.Capacity != nil && c.Pool.Capacity == *r.Capacity && c.Pool.Revision == 1
	}
	if c.Previous == nil || r.Revision == nil || *r.Revision <= 0 || *r.Revision == math.MaxInt64 || c.Pool.Capacity < 0 || c.Previous.Capacity < 0 || c.Pool.ID != r.ID || c.Previous.ID != r.ID || c.Previous.Revision != *r.Revision || c.Pool.Revision != *r.Revision+1 {
		return false
	}
	switch r.Operation {
	case "set":
		return r.Capacity != nil && c.Pool.Capacity == *r.Capacity
	case "adjust":
		return r.Delta != nil && c.Pool.Capacity-c.Previous.Capacity == *r.Delta
	case "drain":
		return c.Pool.State == "draining"
	case "resume":
		return c.Pool.State == "active"
	case "delete":
		return c.Pool.State == "deleted"
	}
	return false
}
func samePoolRequest(a, b model.PoolMutation) bool { return reflect.DeepEqual(a, b) }

func poolPrintable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
