package database

import "time"

// Frozen independent source-history shapes. Producer expansion does not widen
// destructive proof. Pack consistency is checked separately from SSH settlement.
type proofHistoryIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Owner  uint32 `json:"owner"`
	Mode   uint32 `json:"mode"`
}
type proofHistoryMarker struct {
	Schema     int                  `json:"schema"`
	Binding    proofSSHBinding      `json:"binding"`
	CatalogID  string               `json:"catalog_id"`
	Directory  proofHistoryIdentity `json:"directory"`
	Catalog    proofHistoryIdentity `json:"catalog"`
	Packs      proofHistoryIdentity `json:"packs"`
	WorkerLock proofHistoryIdentity `json:"worker_lock"`
}
type proofHistorySelection struct {
	Target string    `json:"target,omitempty"`
	Before time.Time `json:"before,omitempty"`
	Limit  int       `json:"limit"`
}
type proofHistoryMember struct {
	Source          string                `json:"source"`
	Target          string                `json:"target"`
	Key             string                `json:"key"`
	Kind            string                `json:"kind"`
	File            string                `json:"file"`
	Identity        proofHistoryIdentity  `json:"identity"`
	SHA256          string                `json:"sha256"`
	IntentSHA256    string                `json:"intent_sha256"`
	Bytes           int64                 `json:"bytes"`
	PendingFile     string                `json:"pending_file,omitempty"`
	PendingIdentity *proofHistoryIdentity `json:"pending_identity,omitempty"`
	PendingSHA256   string                `json:"pending_sha256,omitempty"`
}
type proofHistoryPlan struct {
	Schema       int                   `json:"schema"`
	Binding      proofSSHBinding       `json:"binding"`
	Certificate  string                `json:"source_certificate"`
	CatalogID    string                `json:"catalog_id"`
	Selection    proofHistorySelection `json:"selection"`
	RosterSHA256 string                `json:"roster_sha256"`
	HotBefore    int                   `json:"hot_before"`
	ColdBefore   int                   `json:"cold_before"`
	Bytes        int64                 `json:"bytes"`
	Members      []proofHistoryMember  `json:"members"`
	SHA256       string                `json:"plan_sha256"`
}
type proofHistoryRequest struct {
	Schema int              `json:"schema"`
	Key    string           `json:"key"`
	Reason string           `json:"reason"`
	Plan   proofHistoryPlan `json:"plan"`
}
type proofHistoryActor struct {
	UID      string
	Username string
}
type proofHistoryPackReceipt struct {
	ID             string               `json:"pack_id"`
	Authority      string               `json:"authority"`
	Root           proofHistoryIdentity `json:"root"`
	Lock           proofHistoryIdentity `json:"lock"`
	Directory      proofHistoryIdentity `json:"directory"`
	Records        proofHistoryIdentity `json:"records"`
	Manifest       proofHistoryIdentity `json:"manifest"`
	RecordsBytes   uint64               `json:"records_bytes"`
	ManifestBytes  uint64               `json:"manifest_bytes"`
	RecordsSHA256  string               `json:"records_sha256"`
	ManifestSHA256 string               `json:"manifest_sha256"`
}
type proofHistoryOperation struct {
	Schema        int                      `json:"schema"`
	Request       proofHistoryRequest      `json:"request"`
	RequestSHA256 string                   `json:"request_sha256"`
	Actor         proofHistoryActor        `json:"actor"`
	Created       time.Time                `json:"created_at"`
	Updated       time.Time                `json:"updated_at"`
	Revision      int64                    `json:"revision"`
	Phase         string                   `json:"phase"`
	PackID        string                   `json:"pack_id"`
	Prepared      *proofHistoryPackReceipt `json:"prepared,omitempty"`
	Receipt       *proofHistoryPackReceipt `json:"receipt,omitempty"`
	Cleaned       []string                 `json:"cleaned"`
	Error         string                   `json:"error,omitempty"`
}
type proofHistoryPack struct {
	Schema    int                     `json:"schema"`
	Operation string                  `json:"operation"`
	Members   int                     `json:"members"`
	Receipt   proofHistoryPackReceipt `json:"receipt"`
}
type proofHistoryKey struct {
	Domain    string `json:"domain"`
	Principal string `json:"principal"`
	Value     string `json:"key"`
}
type proofHistoryRecoveryResult struct {
	CatalogSHA256 string `json:"catalog_sha256"`
	CatalogBytes  int64  `json:"catalog_bytes"`
	Packs         int    `json:"packs"`
	Archived      int    `json:"archived_keys"`
}
type proofHistoryRecoveryAttempt struct {
	Key           string                      `json:"key"`
	Reason        string                      `json:"reason"`
	Actor         proofHistoryActor           `json:"actor"`
	Journal       proofHistoryIdentity        `json:"journal"`
	JournalBytes  int64                       `json:"journal_bytes"`
	JournalSHA256 string                      `json:"journal_sha256"`
	Started       time.Time                   `json:"started_at"`
	Completed     *time.Time                  `json:"completed_at,omitempty"`
	Result        *proofHistoryRecoveryResult `json:"result,omitempty"`
}
type proofHistoryRecovery struct {
	Schema   int                           `json:"schema"`
	Marker   proofHistoryMarker            `json:"marker"`
	Attempts []proofHistoryRecoveryAttempt `json:"attempts"`
}

// This specific nested evidence is bounded to256 published two-file packs.
// It is not a generic recursive namespace skip/cleanup mechanism.
type SSHSourceHistoryEvidence struct {
	PacksRoot File                           `json:"packs_root"`
	Lock      File                           `json:"publication_lock"`
	Packs     []SSHSourceHistoryPackEvidence `json:"packs"`
	Bytes     int64                          `json:"evidence_bytes"`
}
type SSHSourceHistoryPackEvidence struct {
	Directory File   `json:"directory"`
	Files     []File `json:"files"`
}
