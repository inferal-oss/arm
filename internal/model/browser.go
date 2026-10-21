package model

import "time"

// BrowserLink grants one short-lived bootstrap exchange, never the primary
// local discovery credential. The fragment is removed before API requests.
type BrowserLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}
