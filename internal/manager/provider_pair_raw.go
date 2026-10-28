package manager

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"getarm.org/internal/providerpreparation"
)

// Unused source core. These rows are observations, never admission or native
// authority. Only a future reviewed transaction publisher may save originals.
// No get/list result, JSON remarshal, or caller-provided wire value is accepted
// as a substitute for the protected SQL tuple and budget sidecar.
type providerPairLeaseRow struct {
	id, requestKey string
	lease, budget  []byte
}

func providerPairHexID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func providerPairRequestKey(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

func (r providerPairLeaseRow) bounded() bool {
	return providerPairHexID(r.id) && providerPairRequestKey(r.requestKey) &&
		len(r.lease) > 0 && len(r.lease) <= providerpreparation.MaxLeaseBytes &&
		len(r.budget) > 0 && len(r.budget) <= providerpreparation.MaxBudgetBytes
}

func (r providerPairLeaseRow) same(original providerPairLeaseRow) bool {
	return r.bounded() && original.bounded() && r.id == original.id && r.requestKey == original.requestKey &&
		bytes.Equal(r.lease, original.lease) && bytes.Equal(r.budget, original.budget)
}

// readProviderPairLeaseRow requires one caller-owned read snapshot for both
// members. All returned byte projections are capped in SQLite, before Scan.
// Missing/non-TEXT sidecars, SQL run values other than NULL, and any truncation
// refuse. This does not yet decode/approve lease semantics or journal bindings.
func readProviderPairLeaseRow(ctx context.Context, tx *sql.Tx, id string) (providerPairLeaseRow, error) {
	var result providerPairLeaseRow
	if ctx == nil || tx == nil || !providerPairHexID(id) {
		return result, fmt.Errorf("original pair read snapshot and bounded SQL identity required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var rowID, key, raw []byte
	var idType, keyType, dataType string
	var runNull bool
	var size sql.NullInt64
	if err := tx.QueryRowContext(ctx, providerpreparation.ReadLeaseSQL, id).Scan(&rowID, &idType, &key, &keyType, &runNull, &dataType, &size, &raw); err != nil {
		return result, fmt.Errorf("protected pair lease unavailable: %w", err)
	}
	if idType != "text" || string(rowID) != id || keyType != "text" || !providerPairRequestKey(string(key)) || !runNull || dataType != "text" || !size.Valid || size.Int64 <= 0 || size.Int64 > providerpreparation.MaxLeaseBytes || int64(len(raw)) != size.Int64 {
		return result, fmt.Errorf("protected pair lease tuple/type/byte bound changed")
	}
	result = providerPairLeaseRow{id: id, requestKey: string(key), lease: raw}
	var budgetID []byte
	if err := tx.QueryRowContext(ctx, providerpreparation.ReadBudgetSQL, id).Scan(&budgetID, &idType, &dataType, &size, &raw); err != nil {
		return providerPairLeaseRow{}, fmt.Errorf("protected pair budget unavailable: %w", err)
	}
	if idType != "text" || string(budgetID) != id || dataType != "text" || !size.Valid || size.Int64 <= 0 || size.Int64 > providerpreparation.MaxBudgetBytes || int64(len(raw)) != size.Int64 {
		return providerPairLeaseRow{}, fmt.Errorf("protected pair budget tuple/type/byte bound changed")
	}
	result.budget = raw
	if err := ctx.Err(); err != nil {
		return providerPairLeaseRow{}, err
	}
	return result, nil
}
