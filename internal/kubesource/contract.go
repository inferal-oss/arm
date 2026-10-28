package kubesource

// ContractKey is an additive stopped-proof compatibility fence, not a claim
// that older runtime writers refuse startup or that a schema rollout occurred.
const ContractKey = "kubernetes_source_contract"
const ContractValue = `{"version":2,"profiles":["s","i","t","e","f"],"roles":["ks1","kd1","kp1"]}`

func (s Scope) RequiresContract() bool {
	return s.Stage == Start || s.Stage == Bootstrap || s.Stage == Forward
}

// Binding is cached original constructor observation. These IDs cannot mint a
// factory, recover a native loan or prove that current entitlement remains.
type Binding struct {
	Operation      string `json:"operation"`
	Attempt        string `json:"attempt"`
	Stage          string `json:"stage"`
	BinaryBytes    int64  `json:"binary_bytes"`
	SourceLeaseID  string `json:"source_lease_id"`
	StorageLeaseID string `json:"storage_lease_id"`
	PortLeaseID    string `json:"port_lease_id,omitempty"`
}

// Result separates original source closure, acknowledged resource settlement
// and ordinary storage debt. It is observation, never a recovery capability.
type Result struct {
	SourceLeaseID   string `json:"source_lease_id"`
	StorageLeaseID  string `json:"storage_lease_id,omitempty"`
	PortLeaseID     string `json:"port_lease_id,omitempty"`
	PortClosed      bool   `json:"port_closed,omitempty"`
	PortSettled     bool   `json:"port_settled,omitempty"`
	PortState       string `json:"port_state,omitempty"`
	SourceClosed    bool   `json:"source_closed"`
	SourceSettled   bool   `json:"source_settled"`
	StorageState    string `json:"storage_state,omitempty"`
	StorageRetained bool   `json:"storage_retained"`
	PromisedDisk    int64  `json:"promised_disk_bytes"`
}
