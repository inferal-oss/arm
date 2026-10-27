package manager

import (
	"encoding/json"
	"fmt"
	"slices"

	"getarm.org/internal/model"
)

const poolJournalPageBytes = 1 << 20

func (m *Manager) PoolHistoryPage(request model.PoolHistoryPageRequest) (model.PoolHistoryPage, error) {
	result := model.PoolHistoryPage{Pool: request.ID, Entries: []model.PoolChange{}}
	if request.ID == "" || len(request.ID) > 128 || request.After != nil && request.Before != nil || request.After != nil && *request.After < 0 || request.Before != nil && *request.Before <= 0 {
		return result, model.Err("invalid_request", "select a pool ID and one nonnegative after or positive before journal boundary")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.pool(request.ID); err != nil {
		return result, err
	}
	boundary := int64(0)
	condition, order := ">", "ASC"
	if request.After != nil {
		boundary = *request.After
	}
	if request.Before != nil {
		boundary = *request.Before
		condition, order = "<=", "DESC"
	}
	// Do not materialize an oversized corrupt blob merely to reject its JSON.
	rows, err := m.db.Query(fmt.Sprintf(`SELECT sequence,length(CAST(data AS BLOB)),CASE WHEN length(CAST(data AS BLOB))<=? THEN data END FROM pool_changes WHERE pool=? AND sequence%s? ORDER BY sequence %s LIMIT 100`, condition, order), poolJournalPageBytes, request.ID, boundary)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	size := 1024 // Bound encoded result metadata and array separators too.
	for rows.Next() {
		var entry model.PoolChange
		var sequence int64
		var rawSize int64
		var raw []byte
		if err = rows.Scan(&sequence, &rawSize, &raw); err != nil {
			return result, err
		}
		if rawSize > poolJournalPageBytes {
			if len(result.Entries) > 0 {
				break
			}
			return result, model.Err("response_too_large", "one complete journal entry exceeds the 1 MiB page budget; no entry was truncated")
		}
		if err = json.Unmarshal(raw, &entry); err != nil {
			return result, err
		}
		if sequence <= 0 || entry.Pool.ID != request.ID || entry.Pool.Authority != m.host || entry.Pool.Owner != m.owner || entry.Previous != nil && (entry.Previous.ID != request.ID || entry.Previous.Authority != m.host || entry.Previous.Owner != m.owner) {
			return result, model.Err("invalid_accounting", "journal entry identity does not match its local pool ledger")
		}
		entry.Sequence = sequence
		encoded, e := json.Marshal(entry)
		if e != nil {
			return result, e
		}
		if len(encoded)+1024 > poolJournalPageBytes {
			if len(result.Entries) > 0 {
				break
			}
			return result, model.Err("response_too_large", "one complete journal entry exceeds the 1 MiB page budget; no entry was truncated")
		}
		if len(result.Entries) == 100 || size+len(encoded)+1 > poolJournalPageBytes {
			break
		}
		result.Entries = append(result.Entries, entry)
		size += len(encoded) + 1
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if request.Before != nil {
		slices.Reverse(result.Entries)
	}
	exists := func(condition string, sequence int64) (bool, error) {
		var found bool
		err := m.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pool_changes WHERE pool=? AND sequence`+condition+`?)`, request.ID, sequence).Scan(&found)
		return found, err
	}
	if len(result.Entries) == 0 {
		if request.Before != nil {
			more, e := exists(">", boundary)
			if e != nil {
				return result, e
			}
			if more {
				next := boundary
				result.NextAfter = &next
			}
		} else if boundary > 0 {
			more, e := exists("<=", boundary)
			if e != nil {
				return result, e
			}
			if more {
				previous := boundary
				result.PreviousBefore = &previous
			}
		}
		return result, nil
	}
	first, last := result.Entries[0].Sequence, result.Entries[len(result.Entries)-1].Sequence
	if more, e := exists("<", first); e != nil {
		return result, e
	} else if more {
		previous := first - 1
		result.PreviousBefore = &previous
	}
	if more, e := exists(">", last); e != nil {
		return result, e
	} else if more {
		next := last
		result.NextAfter = &next
	}
	return result, nil
}
