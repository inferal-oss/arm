package tui

import "getarm.org/internal/model"

// Pool read projections add labels outside the exact PoolView/consumer models
// used for accounting, review hashes, revisions and mutation requests.
type poolRenderView struct {
	model.PoolView
	DisplayName string          `json:"display_name"`
	Users       []useRenderView `json:"users"`
	Queued      []useRenderView `json:"queued"`
}

func (v poolRenderView) values() (model.PoolView, renderLabels) {
	p := v.PoolView
	names := renderLabels{authority: p.Pool.Authority}
	if v.DisplayName != "" {
		names.resources = map[string]string{p.Pool.ID: v.DisplayName}
	}
	copyUses := func(views []useRenderView) []model.ResourceUse {
		if views == nil {
			return nil
		}
		uses := make([]model.ResourceUse, len(views))
		for i, u := range views {
			uses[i] = u.ResourceUse
			names.addLease(model.Lease{ID: u.Lease, Host: p.Pool.Authority}, u.LeaseDisplayName, "")
		}
		return uses
	}
	if v.Users != nil {
		p.Users = copyUses(v.Users)
	}
	if v.Queued != nil {
		p.Queued = copyUses(v.Queued)
	}
	return p, names
}

type poolConsumerRenderView struct {
	model.PoolConsumer
	Use useRenderView `json:"use"`
}
type poolConsumersRenderView struct {
	model.PoolConsumerPage
	Entries []poolConsumerRenderView `json:"entries"`
}

func (v poolConsumersRenderView) values(authority string) (model.PoolConsumerPage, renderLabels) {
	p := v.PoolConsumerPage
	names := renderLabels{authority: authority}
	if v.Entries != nil {
		p.Entries = make([]model.PoolConsumer, len(v.Entries))
		for i, entry := range v.Entries {
			p.Entries[i] = entry.PoolConsumer
			p.Entries[i].Use = entry.Use.ResourceUse
			names.addLease(model.Lease{ID: entry.Use.Lease, Host: authority}, entry.Use.LeaseDisplayName, "")
		}
	}
	return p, names
}
