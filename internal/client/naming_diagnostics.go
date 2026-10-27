package client

import "getarm.org/internal/model"

// Naming diagnostics are cosmetic, bounded and never substitute for an action
// result. Do not echo arbitrary peer-provided text into operator interfaces.
func namingDiagnostic(diagnostic *model.Error) *model.Error {
	if diagnostic == nil {
		return nil
	}
	if diagnostic.Code == "name_metadata_invalid" {
		return &model.Error{Code: "name_metadata_invalid", Message: "Custom name metadata is invalid; exact IDs remain available. Inspect the database without repairing or dropping evidence."}
	}
	return &model.Error{Code: "name_metadata_unavailable", Message: "Custom names are unavailable; exact IDs remain available. Read a fresh naming snapshot before using custom selectors."}
}
