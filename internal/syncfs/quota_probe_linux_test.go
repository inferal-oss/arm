//go:build linux && (amd64 || arm64)

package syncfs

import (
	"errors"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestXFSQuotaPermissionDenialIsUnknown(t *testing.T) {
	if err := xfsQuotaRefusal(unix.EPERM); !errors.Is(err, ErrQuotaUnknown) || errors.Is(err, ErrQuotaUnsupported) {
		t.Fatalf("project quota permission denial was not unknown: %v", err)
	}
}

func TestXFSQuotaABISizes(t *testing.T) {
	if xqmGetQuota != 0x580302 || xqmGetStatusV != 0x580802 || fsIOCFSGetXattr != 0x801c581f {
		t.Fatal("XFS quota command ABI differs from the Linux UAPI")
	}
	if got := unsafe.Sizeof(xfsAttr{}); got != 28 {
		t.Fatalf("fsxattr ABI size = %d", got)
	}
	if got := unsafe.Sizeof(xfsQuotaStatus{}); got != 160 {
		t.Fatalf("fs_quota_statv ABI size = %d", got)
	}
	if got := unsafe.Sizeof(xfsDiskQuota{}); got != 112 {
		t.Fatalf("fs_disk_quota ABI size = %d", got)
	}
}

func TestXFSQuotaEvidenceRefusesAccountingOnlyAndChangedScope(t *testing.T) {
	selected := xfsAttr{Flags: fsXflagProjInherit, ProjectID: 42}
	parent := selected
	status := xfsQuotaStatus{Version: 1, Flags: fsQuotaProjectAcct | fsQuotaProjectEnfd}
	quota := xfsDiskQuota{Version: 1, Flags: fsProjectQuotaType, ID: 42, BlockHard: 1024, Blocks: 128}
	v, err := xfsEvidenceFromReadings("mount-1", selected, parent, status, quota)
	if err != nil || v.Scope() != "xfs-project:mount-1:42" || v.HardBytes() != 1024*512 || v.UsedBytes() != 128*512 {
		t.Fatalf("enforced project quota evidence: %+v %v", v, err)
	}
	status.Flags = fsQuotaProjectAcct
	if v, err := xfsEvidenceFromReadings("mount-1", selected, parent, status, quota); !errors.Is(err, ErrQuotaUnsupported) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("accounting-only project accepted: %+v %v", v, err)
	}
	status.Flags |= fsQuotaProjectEnfd
	for _, wrongType := range []int8{3, 6, -1} {
		quota.Flags = wrongType
		if v, err := xfsEvidenceFromReadings("mount-1", selected, parent, status, quota); !errors.Is(err, ErrQuotaUnknown) || v != (NativeQuotaEvidence{}) {
			t.Fatalf("malformed project quota type %d accepted: %+v %v", wrongType, v, err)
		}
	}
	quota.Flags = fsProjectQuotaType
	parent.ProjectID++
	if v, err := xfsEvidenceFromReadings("mount-1", selected, parent, status, quota); !errors.Is(err, ErrQuotaUnsupported) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("changed project accepted: %+v %v", v, err)
	}
	parent = selected
	quota.BlockHard = 0
	if v, err := xfsEvidenceFromReadings("mount-1", selected, parent, status, quota); !errors.Is(err, ErrQuotaUnsupported) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("unlimited project accepted: %+v %v", v, err)
	}
}
