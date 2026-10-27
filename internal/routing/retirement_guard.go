package routing

import (
	"context"
	"database/sql"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
)

// CheckWorkspaceRetirement is an internal native authority-reducing producer
// guard. It validates the COMPLETE optional routing schema and records inside
// the PARENT retirement transaction, and never initializes or grants anything.
// Retiring current membership must not strand exact live receipt lookup.
func CheckWorkspaceRetirement(ctx context.Context, tx *sql.Tx, a workspace.Authority, id string) error {
	v, e := Read(ctx, tx)
	if e != nil {
		return e
	}
	for _, root := range v.Roots {
		if root.Workspace == id && root.Authority != a {
			return model.Err("forbidden", "routed retirement guard belongs to another native authority")
		}
	}
	for _, o := range v.Outbox {
		if o.Workspace == id && !known(o.Phase, "completed", "cancelled") {
			detail := "Reconcile the original pinned destination receipt with message-lookup --reconcile before retiring; dispatched/unknown delivery cannot be cancelled or silently settled."
			if o.Phase == "prepared" {
				detail = "Cancel this NEVER-dispatched intention with message-cancel and its exact revision before retiring."
			}
			return model.Err("obligations_unresolved", "Workspace retirement would remove live outcome-lookup authority for routed key "+o.Intent.Key+". "+detail)
		}
	}
	for _, in := range v.Inbox {
		if in.Workspace == id && in.Receipt.State == "delivery_pending" {
			return model.Err("obligations_unresolved", "Pending routed endpoint delivery must settle before local membership retirement; no native turn was retried")
		}
	}
	return nil
}
