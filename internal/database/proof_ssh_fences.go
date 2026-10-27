package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

const sshFenceResetSchema = `CREATE TABLE ssh_key_fences (principal TEXT NOT NULL,key TEXT NOT NULL,control_sha256 TEXT NOT NULL,state TEXT NOT NULL,lease TEXT UNIQUE,data TEXT NOT NULL,PRIMARY KEY(principal,key))`

type proofSSHControl struct {
	Schema          int             `json:"schema"`
	Source          string          `json:"source"`
	Target          string          `json:"target"`
	Pair            string          `json:"pair"`
	Revision        int             `json:"revision"`
	Certificate     string          `json:"certificate"`
	Owner           string          `json:"owner"`
	Account         string          `json:"account"`
	HostKeySHA256   string          `json:"host_key_sha256"`
	Key             string          `json:"key"`
	PublicKeySHA256 string          `json:"public_key_sha256"`
	Reason          string          `json:"reason"`
	TTL             time.Duration   `json:"ttl_ns"`
	Resources       model.Resources `json:"resources"`
	CWD             string          `json:"cwd"`
	Kind            string          `json:"kind"`
	CommandSHA256   string          `json:"command_sha256"`
	PTY             bool            `json:"pty"`
}
type proofSSHKeyFence struct {
	Schema          int              `json:"schema"`
	Source          string           `json:"source"`
	Target          string           `json:"target"`
	Key             string           `json:"key"`
	State           string           `json:"state"`
	Control         *proofSSHControl `json:"control"`
	ControlSHA256   string           `json:"control_sha256"`
	OperationSHA256 string           `json:"operation_sha256"`
	Lease           string           `json:"lease"`
	Actor           string           `json:"actor"`
	Reason          string           `json:"reason"`
	At              time.Time        `json:"at"`
}
type proofSSHSourceSeal struct {
	Schema int              `json:"schema"`
	Intent proofSSHSource   `json:"intent"`
	Seal   proofSSHKeyFence `json:"seal"`
	At     time.Time        `json:"settled_at"`
}
type proofSSHOperationEvidence struct {
	operation model.SSHOperation
	raw       string
}

func proofSSHOperationKey(source, key string) string { return source + "\x00" + key }
func (p *proofReader) readSSHFences(operations map[string]proofSSHOperationEvidence) error {
	_, present := p.catalog["ssh_key_fences"]
	var marker string
	e := p.tx.QueryRowContext(p.ctx, `SELECT value FROM metadata WHERE key='ssh_fence_schema'`).Scan(&marker)
	if !present && errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if !present || e != nil || marker != "1" || !p.sshSchema {
		return fmt.Errorf("SSH key fence marker/catalog/operation group differs")
	}
	seen := map[string]bool{}
	if e = p.walk("ssh_key_fences", func(row proofRow) error {
		raw, e := row.text("data")
		if e != nil {
			return e
		}
		var mirror proofSSHKeyFence
		if e = strictSSHProducer([]byte(raw), &mirror); e != nil {
			return e
		}
		var fence model.SSHKeyFence
		if e = json.Unmarshal([]byte(raw), &fence); e != nil || fence.Validate() != nil {
			return fmt.Errorf("invalid complete SSH key fence")
		}
		source, e := row.text("principal")
		if e != nil {
			return e
		}
		key, e := row.text("key")
		if e != nil {
			return e
		}
		state, e := row.text("state")
		if e != nil {
			return e
		}
		digest, e := row.text("control_sha256")
		if e != nil {
			return e
		}
		lease := ""
		if row["lease"] != nil {
			lease, e = row.text("lease")
			if e != nil {
				return e
			}
		}
		if source != fence.Source || key != fence.Key || state != fence.State || digest != fence.ControlSHA256 || lease != fence.Lease || fence.Target != p.result.Host || (fence.Actor != "peer:"+source && fence.Actor != "local:"+p.result.Owner) {
			return fmt.Errorf("SSH key fence row/authority differs")
		}
		qualified := proofSSHOperationKey(source, key)
		if seen[qualified] {
			return fmt.Errorf("duplicate SSH key fence")
		}
		seen[qualified] = true
		op, exists := operations[qualified]
		if fence.State == "sealed" {
			if exists || fence.Control.Owner != p.result.Owner {
				return fmt.Errorf("nonexecution SSH fence overlaps admission or another owner")
			}
			for _, l := range p.leases {
				if l.Request.Key == mesh.Key(source, "ssh-exec\x00"+key) {
					return fmt.Errorf("nonexecution SSH key has a lease")
				}
			}
			return nil
		}
		if !exists || op.operation.Lease != fence.Lease || proofDigest([]byte(op.raw)) != fence.OperationSHA256 {
			return fmt.Errorf("SSH admitted/legacy fence lacks its exact original operation")
		}
		l, exists := p.leases[fence.Lease]
		if !exists {
			return fmt.Errorf("SSH fenced lease missing")
		}
		if fence.State == "admitted" && (fence.Control.Owner != l.Owner || !fence.Control.Matches(op.operation.Request) || !fence.At.Equal(op.operation.AdmittedAt)) {
			return fmt.Errorf("SSH admitted fence differs from original control or admission")
		}
		return nil
	}); e != nil {
		return e
	}
	for key := range operations {
		if !seen[key] {
			return fmt.Errorf("SSH operation missing from complete fenced key namespace")
		}
	}
	return nil
}
