package server

import (
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

// Read-only wrappers preserve the original review and accounting objects.
// Mutation and lookup receipts never pass through this projection.
type namedPoolView struct {
	model.PoolView
	DisplayName string             `json:"display_name,omitempty"`
	Users       []namedResourceUse `json:"users"`
	Queued      []namedResourceUse `json:"queued"`
}

type namedPoolConsumer struct {
	model.PoolConsumer
	Use namedResourceUse `json:"use"`
}

type namedPoolConsumerPage struct {
	model.PoolConsumerPage
	Entries []namedPoolConsumer `json:"entries"`
}

func namedPoolUses(users []model.ResourceUse) []namedResourceUse {
	if users == nil {
		return nil
	}
	view := make([]namedResourceUse, len(users))
	for i, use := range users {
		view[i] = namedResourceUse{ResourceUse: use, LeaseDisplayName: readablename.FromID(readablename.Lease, use.Lease)}
	}
	return view
}

func poolReadView(value model.PoolView) namedPoolView {
	return namedPoolView{PoolView: value, Users: namedPoolUses(value.Users), Queued: namedPoolUses(value.Queued)}
}

func namedPoolRead(value any) any {
	switch value := value.(type) {
	case model.PoolView:
		return poolReadView(value)
	case []model.PoolView:
		if value == nil {
			return []namedPoolView(nil)
		}
		view := make([]namedPoolView, len(value))
		for i, pool := range value {
			view[i] = poolReadView(pool)
		}
		return view
	case model.PoolConsumerPage:
		view := namedPoolConsumerPage{PoolConsumerPage: value}
		if value.Entries != nil {
			view.Entries = make([]namedPoolConsumer, len(value.Entries))
			for i, entry := range value.Entries {
				view.Entries[i] = namedPoolConsumer{PoolConsumer: entry, Use: namedResourceUse{ResourceUse: entry.Use, LeaseDisplayName: readablename.FromID(readablename.Lease, entry.Use.Lease)}}
			}
		}
		return view
	}
	return value
}
