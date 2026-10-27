package database

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
)

const sshResetSchema = `CREATE TABLE ssh_operations (principal TEXT NOT NULL,key TEXT NOT NULL,lease TEXT NOT NULL UNIQUE,data TEXT NOT NULL,PRIMARY KEY(principal,key))`

type proofSSHRun struct {
	model.Request
	Command []string                `json:"command"`
	CWD     string                  `json:"cwd"`
	SSH     *model.SSHRunDescriptor `json:"ssh,omitempty"`
}

func (p *proofReader) readSSH() error {
	if err := checkSSHProofTypeShapes(); err != nil {
		return err
	}
	_, exists := p.catalog["ssh_operations"]
	if p.sshSchema != exists {
		return fmt.Errorf("SSH operation schema and marker do not match")
	}
	seen := map[string]bool{}
	operations := map[string]proofSSHOperationEvidence{}
	if err := p.walk("ssh_operations", func(row proofRow) error {
		op, err := rowJSON[model.SSHOperation](row, "data")
		if err != nil {
			return err
		}
		source, err := row.text("principal")
		if err != nil {
			return err
		}
		key, err := row.text("key")
		if err != nil {
			return err
		}
		id, err := row.text("lease")
		if err != nil {
			return err
		}
		q := op.Request
		if op.Schema != 1 || !validHost(source) || source != q.Source || key != q.Key || !mesh.ValidKey(key) || id != op.Lease || !evidenceID(id) || seen[id] || q.Target != p.result.Host || !sshProofHex(q.Pair, 32) || q.Revision < 1 || !sshProofHex(q.Certificate, 32) || !sshProofHex(q.PublicKeySHA256, 32) || !evidenceID(q.Account) || len(q.Account) > 64 || q.Account == "" || !oneOf(q.Kind, "exec", "shell") || !sshProofHex(q.CommandSHA256, 32) || q.TerminalSHA256 != "" && !sshProofHex(q.TerminalSHA256, 32) || !validResources(q.Resources) || q.Resources == (model.Resources{}) || q.TTL <= 0 || q.TTL > 7*24*time.Hour || len(strings.TrimSpace(q.Reason)) == 0 || len(q.Reason) > 4096 || !filepath.IsAbs(q.CWD) || len(q.CWD) > 4096 || op.AdmittedAt.IsZero() {
			return fmt.Errorf("invalid SSH admission identity/commitments")
		}
		l, ok := p.leases[id]
		if !ok {
			return fmt.Errorf("SSH admission references missing lease")
		}
		if err = sshservice.ValidateSSHLeaseAttachments(l); err != nil {
			return err
		}
		run, ok := p.sshRuns[id]
		if !ok || run.SSH == nil {
			return fmt.Errorf("SSH admission lacks redacted run evidence")
		}
		want := &model.SSHRunDescriptor{Schema: 1, Source: q.Source, Key: q.Key, Kind: q.Kind, CommandSHA256: q.CommandSHA256, TerminalSHA256: q.TerminalSHA256}
		if !reflect.DeepEqual(run.SSH, want) || !reflect.DeepEqual(run.Command, []string{"[arm-ssh:redacted]"}) || run.CWD != q.CWD || !reflect.DeepEqual(run.Request, l.Request) || q.Account != l.OwnerUsername || l.Request.Key != mesh.Key(q.Source, "ssh-exec\x00"+q.Key) || l.Request.Peer != q.Source || l.Request.OriginKey != q.Key || l.Request.PeerCertificate != q.Certificate || l.Request.PeerGrantRevision != q.Revision || l.Request.Resources != q.Resources || l.Request.TTL != q.TTL || l.Request.Reason != q.Reason || l.Request.Kind != "" || l.Request.Queue || l.Request.StartDeadline != nil || l.Request.StorageLease != "" || l.Request.PortLease != "" || l.Request.ServiceLease != "" || len(l.Request.Ports) > 0 || len(l.Request.Pools) > 0 || l.Request.Agent != "" || l.Request.Profile != "" || op.AdmittedAt.Before(l.Created) || l.Ended != nil && op.AdmittedAt.After(*l.Ended) {
			return fmt.Errorf("SSH admission/lease/run provenance differs")
		}
		if run.SSH.Schema != 1 {
			return fmt.Errorf("unknown SSH run descriptor schema")
		}
		seen[id] = true
		raw, err := row.text("data")
		if err != nil {
			return err
		}
		operations[proofSSHOperationKey(source, key)] = proofSSHOperationEvidence{op, raw}
		return nil
	}); err != nil {
		return err
	}
	for id := range p.sshRuns {
		if !seen[id] {
			return fmt.Errorf("redacted SSH run lacks durable admission")
		}
	}
	return p.readSSHFences(operations)
}

func sshProofHex(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n && s == strings.ToLower(s)
}
