package model

// PoolHistoryPageRequest chooses one exact journal boundary. After is exclusive;
// Before is inclusive, allowing an empty after=MaxInt64 page to go backward.
type PoolHistoryPageRequest struct {
	ID     string `json:"id"`
	After  *int64 `json:"after,omitempty"`
	Before *int64 `json:"before,omitempty"`
}

// Entries are always ascending. Cursors navigate observations of the immutable
// journal; a page is never an authorization scope for a pool mutation.
type PoolHistoryPage struct {
	Pool           string       `json:"pool"`
	Entries        []PoolChange `json:"entries"`
	PreviousBefore *int64       `json:"previous_before"`
	NextAfter      *int64       `json:"next_after"`
}
