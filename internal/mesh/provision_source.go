package mesh

import (
	"encoding/json"
	"time"

	"getarm.org/internal/model"
)

// RecordProvisionLookup settles a positively verified original native receipt.
// The pure typed validator runs again against the exact locked source row. No
// native work occurs under this lock and no original worker duty is cleared.
func RecordProvisionLookup(home, key string, revision int64, result json.RawMessage, validate func(Coordination, json.RawMessage) error) (Coordination, error) {
	unlock, e := coordinationLock(home)
	if e != nil {
		return Coordination{}, e
	}
	defer unlock()
	op, exists, e := ReadCoordination(home, key)
	if e != nil {
		return op, e
	}
	if !exists || op.Kind != "provision" || op.Revision != revision || !op.Approved || validate == nil {
		return op, model.Err("review_changed", "exact approved original provisioning revision required")
	}
	if e = validate(op, result); e != nil {
		return op, e
	}
	if op.Error != nil && op.Error.Code == "provision_source_cleanup_unknown" {
		return op, model.Err("source_owner_unresolved", "native target observation cannot settle original source reader cleanup")
	}
	if op.Phase == "completed" && op.Worker == "" && op.Error == nil {
		return op, nil
	}
	if op.Worker != "" {
		return op, model.Err("source_owner_unresolved", "target receipt was observed but original source worker has no retained join; its duty remains")
	}
	if op.Revision == 1<<63-1 {
		return op, model.Err("history_full", "provisioning source revision exhausted")
	}
	op.Result = append(json.RawMessage(nil), result...)
	op.Phase = "completed"
	op.Error = nil
	op.Revision++
	op.Updated = time.Now().UTC()
	return op, saveCoordination(home, op)
}
