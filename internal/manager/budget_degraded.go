package manager

import (
	"errors"
	"github.com/inferal-oss/arm/internal/model"
)

// Only identifiable policy-accounting failures enter this narrow startup mode.
// General SQLite/schema/provider failures retain their ordinary Open behavior.
// The original ledger is never repaired, rebound or replaced with unlimited
// policy. Degradation stays latched for this backend lifetime; restart after
// restoring evidence (or settling the affected obligations) revalidates it.
func (m *Manager) degradeBudgetStartup(err error) bool {
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "invalid_accounting" {
		return false
	}
	detail := api.Message
	if len(detail) > 3072 {
		detail = detail[:3072]
	}
	m.budgetStartupError = &model.Error{Code: "invalid_accounting", Message: detail + ". Policy accounting is degraded: new grants, renewals and policy changes are paused. Inspection and ordinary provider-safe release, expiry, reconciliation and handoff remain available; no policy records were repaired."}
	return true
}
func (m *Manager) budgetPolicyError() *model.Error {
	if m.budgetStartupError == nil {
		return nil
	}
	copy := *m.budgetStartupError
	return &copy
}
