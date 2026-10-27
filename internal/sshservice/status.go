package sshservice

import (
	"bytes"
	"encoding/json"
	"fmt"

	"getarm.org/internal/model"
)

// DecodeKeyStatus accepts the exact closed producer representation, including
// required false/zero/null fields. encoding/json's aliases, duplicate keys and
// primitive-null coercions cannot silently become settlement evidence.
func DecodeKeyStatus(raw []byte, out *model.SSHKeyStatus) error {
	if out == nil || len(raw) > 60<<10 {
		return fmt.Errorf("bounded SSH key status required")
	}
	var next model.SSHKeyStatus
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&next); e != nil {
		return e
	}
	canonical, e := json.Marshal(next)
	if e != nil || !bytes.Equal(bytes.TrimSpace(raw), canonical) {
		return fmt.Errorf("SSH key status differs from exact canonical producer")
	}
	*out = next
	return nil
}

// ValidateKeyStatus checks the complete envelope BEFORE source cleanup or
// settlement decisions. Valid absent/nonterminal statuses remain UNKNOWN; only
// the existing exact terminal/seal matchers can authorize actual settlement.
// The legacy variant explicitly supports an older operation namespace without
// inventing a historical fence or control commitment.
func ValidateKeyStatus(intent SourceIntent, status model.SSHKeyStatus) error {
	if intent.Validate() != nil || status.Schema != 1 || status.Source != intent.Source || status.Target != intent.Request.Pin.Target || status.Key != intent.Request.Key {
		return fmt.Errorf("SSH key status schema/authority/key differs from exact source intention")
	}
	switch status.State {
	case "absent":
		if status.Fence != nil || status.Execution != nil {
			return fmt.Errorf("absent SSH status contains contradictory evidence")
		}
		return nil
	case "sealed":
		if status.Fence == nil || status.Execution != nil {
			return fmt.Errorf("sealed SSH status must contain only one nonexecution fence")
		}
		if e := MatchSourceSeal(intent, *status.Fence); e != nil {
			return e
		}
		if status.Fence.Actor != "peer:"+intent.Source && status.Fence.Actor != "local:"+intent.Request.TargetOwner {
			return fmt.Errorf("SSH seal actor differs from destination authority")
		}
		return nil
	case "admitted", "legacy-admitted":
		if status.Execution == nil {
			return fmt.Errorf("admitted SSH status lacks exact operation and lease")
		}
		if e := matchSSHAdmission(intent, *status.Execution, false); e != nil {
			return e
		}
		if status.Fence == nil {
			if status.State != "legacy-admitted" {
				return fmt.Errorf("admitted SSH status lacks its fence")
			}
			return nil
		}
		f := status.Fence
		op := status.Execution.Operation
		l := status.Execution.Lease
		if f.Validate() != nil || f.State != status.State || f.Source != status.Source || f.Target != status.Target || f.Key != status.Key || f.Lease != l.ID {
			return fmt.Errorf("SSH status fence differs from operation/lease identity")
		}
		raw, e := json.Marshal(op)
		if e != nil || f.OperationSHA256 != stateHash(raw) {
			return fmt.Errorf("SSH status fence differs from exact immutable operation")
		}
		if status.State == "admitted" {
			expected, e := intent.ControlCommitment()
			if e != nil {
				return e
			}
			digest, e := expected.Digest()
			if e != nil || f.ControlSHA256 != digest || f.Control == nil || !f.Control.Matches(op.Request) || f.Control.Owner != intent.Request.TargetOwner || !f.At.Equal(op.AdmittedAt) || f.Actor != "peer:"+intent.Source || f.Reason != expected.Reason {
				return fmt.Errorf("SSH admitted fence differs from original control/admission")
			}
		} else if f.Actor != "local:"+intent.Request.TargetOwner {
			return fmt.Errorf("legacy SSH fence has another destination actor")
		}
		return nil
	default:
		return fmt.Errorf("unknown SSH key status state")
	}
}
