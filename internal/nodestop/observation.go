package nodestop

// Prepared supplements the exact authorization plan with destination-reported
// display/account/history facts. The username never replaces UID authority.
type Prepared struct {
	Plan            Plan   `json:"plan"`
	SHA256          string `json:"review_sha256"`
	OwnerUsername   string `json:"owner_username"`
	HistoryUsed     int    `json:"history_used"`
	HistoryCapacity int    `json:"history_capacity"`
}

type Observation struct {
	Record                   Record `json:"record"`
	Desired                  State  `json:"desired_state"`
	ServerJoined             bool   `json:"server_joined"`
	OriginalGenerationAbsent bool   `json:"original_generation_absent"`
}
