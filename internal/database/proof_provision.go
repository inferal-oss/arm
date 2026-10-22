package database

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/yrashk/arm/internal/meshops"
)

func provisionBinary(q meshops.ProvisionRequest) string {
	return q.Transfer.Root + "/artifact-" + q.Transfer.SHA256 + "/arm"
}
func validateProvisionResult(r *proofCoordRecord, v meshops.ProvisionResult) error {
	q := r.request.Provision
	if q == nil {
		return fmt.Errorf("provision result without request")
	}
	receipt := v.Receipt
	runtime := receipt.Runtime
	if v.Key != q.Transfer.Key || v.ConnectionKey != q.ConnectionKey || v.Provisioned != provisionBinary(*q) || v.VerifiedAt.IsZero() || receipt.Schema != 1 || receipt.Request != q.Transfer || receipt.Actor.UID != q.Transfer.ExpectedUID || receipt.Phase != "started_verified" || receipt.Binary != v.Provisioned || receipt.Version != q.Transfer.Version || receipt.Protocol != q.Transfer.Protocol || receipt.Updated.IsZero() || receipt.StartHome != q.Home || receipt.Startup == nil || *receipt.Startup != q.Startup || !validHost(receipt.StartedHost) || runtime == nil {
		return fmt.Errorf("provision outcome is incomplete or differs from reviewed artifact/start")
	}
	if runtime.Host != receipt.StartedHost || runtime.UID != q.Transfer.ExpectedUID || runtime.SHA256 != q.Transfer.SHA256 || runtime.Size != q.Transfer.Size || runtime.Version != q.Transfer.Version || runtime.Protocol != q.Transfer.Protocol || runtime.Captured.IsZero() || runtime.Captured.After(receipt.Updated) || !oneOf(runtime.Method, "kernel-self-executable", "startup-open-file-snapshot") {
		return fmt.Errorf("provision runtime does not prove the exact approved artifact")
	}
	if q.Transfer.OS == "linux" && runtime.Method != "kernel-self-executable" || q.Transfer.OS == "darwin" && runtime.Method != "startup-open-file-snapshot" {
		return fmt.Errorf("runtime proof method differs from target platform")
	}
	return nil
}
func validateProvisionLink(r, root *proofCoordRecord, records map[string]*proofCoordRecord) error {
	c := r.request.Connect
	if c == nil || c.ProvisionKey == "" {
		return nil
	}
	if !validDigest(c.ProvisionKey) {
		return fmt.Errorf("invalid connect provision reference")
	}
	install, ok := records[c.ProvisionKey]
	if !ok || install.op.Kind != "provision" || install.request.Provision == nil || install.op.ParentKey != "" {
		return fmt.Errorf("connect lacks original exact provision receipt")
	}
	q := install.request.Provision
	if q.ConnectionKey != root.op.Key || q.Destination != c.Destination || q.Home != c.RemoteHome || provisionBinary(*q) != c.Binary || q.Transfer.Reason != c.Reason {
		return fmt.Errorf("connect provisioning link changed authority/path/reason")
	}
	settled := install
	if install.op.Phase == "incomplete" && install.op.ResolvedBy != "" {
		settled = records[install.op.ResolvedBy]
	}
	if settled == nil || settled.op.Phase != "completed" || settled.op.Worker != "" || !settled.op.Approved || settled.op.Error != nil || settled.provisionResult == nil || install.provisionReview == nil || !install.op.Approved {
		return fmt.Errorf("connect references an unsettled installation/start")
	}
	if !reflect.DeepEqual(settled.request.Provision, q) {
		return fmt.Errorf("provision recovery changed original installation intent")
	}
	if r.connectReview != nil && (r.connectReview.SSH != q.SSH || r.connectReview.Remote.OS != q.Transfer.OS || r.connectReview.Remote.Arch != q.Transfer.Arch || r.connectReview.Remote.Version != q.Transfer.Version) {
		return fmt.Errorf("connect SSH account/key differs from reviewed installation")
	}
	if r.connectResult != nil {
		remote := r.connectResult.Remote
		if remote.Host != settled.provisionResult.Receipt.StartedHost || remote.Owner != q.Transfer.ExpectedUID || remote.OwnerUsername != "" && strings.TrimSpace(remote.OwnerUsername) == "" {
			return fmt.Errorf("connected node differs from provisioned runtime authority")
		}
	}
	return nil
}
