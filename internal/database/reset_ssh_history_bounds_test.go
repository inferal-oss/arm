package database

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestResetLargestDeclaredSourceHistoryEvidencePlanAndReceiptReopen(t *testing.T) {
	s, r, file := resetFileEngineFixture(t)
	owner := uint32Owner(r.Plan.Actor.UID)
	inode := uint64(100)
	newFile := func(name string, size int64, dir bool) File {
		inode++
		mode := uint32(unix.S_IFREG | 0600)
		if dir {
			mode = unix.S_IFDIR | 0700
		}
		return File{Name: name, Present: true, Device: 123, Inode: inode, Owner: owner, Mode: mode, Size: size, Allocated: size, Modified: 1700000000000000000, Changed: 1700000000000000001, SHA256: strings.Repeat("a", 64)}
	}
	n := NamespaceEvidence{Name: sourceHistoryProofName, Present: true, Identity: newFile(sourceHistoryProofName, 8192, true), Files: []File{newFile(".worker.lock", 0, false), newFile("catalog.db", 64<<20, false)}}
	e := &SSHSourceHistoryEvidence{PacksRoot: newFile("packs", 8192, true), Lock: newFile(".historypack.lock", 0, false), Packs: []SSHSourceHistoryPackEvidence{}, Bytes: 64 << 20}
	e.Lock.SHA256 = hashEmptyResetFile
	n.Files[0].SHA256 = hashEmptyResetFile
	for i := 0; i < 256; i++ {
		e.Packs = append(e.Packs, SSHSourceHistoryPackEvidence{Directory: newFile(fmt.Sprintf("pack-%032x", i+1), 8192, true), Files: []File{newFile("manifest.json", 1<<20, false), newFile("records.bin", 2<<20, false)}})
		e.Bytes += 3 << 20
	}
	n.SSHHistory = e
	if err := validSSHHistoryEvidence(n); err != nil {
		t.Fatal(err)
	}
	r.Plan.Namespaces = append(r.Plan.Namespaces, n)
	r.Protected = append([]NamespaceEvidence{}, r.Plan.Namespaces...)
	r.Plan.RetireMesh = true
	r.Archives[sourceHistoryProofName] = "source"
	r.Plan.Reason = strings.Repeat("\"", 4096)
	var err error
	r.Plan.SHA256, err = planDigest(r.Plan)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := json.Marshal(r.Plan)
	if err != nil || len(plan) > MaxRecordBytes {
		t.Fatal("largest admitted declaration cannot form bounded review", len(plan), err)
	}
	if err = s.updateReceipt(&r, &file); err != nil {
		t.Fatal("largest declared pack roster cannot persist/reopen", err)
	}
	got, _, err := s.readReceipt()
	if err != nil || len(got.Protected[len(got.Protected)-1].SSHHistory.Packs) != 256 {
		t.Fatal("largest complete roster lost on reopen", err)
	}
	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil || len(encoded)+1 > MaxRecordBytes {
		t.Fatal("successful maximum receipt exceeds reader bound", len(encoded), err)
	}
}

func TestResetHistoryCollectionPreflightBeforeTypedAllocation(t *testing.T) {
	s, _, _ := resetStoreFixture(t)
	raw := []byte(`{"protected_namespaces":[{"ssh_source_history":{"packs":[` + strings.Repeat(`{},`, 2000) + `{}]}}]}`)
	if err := os.WriteFile(s.operation.Path+"/collection.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, _, err := readResetJSON[ResetReceipt](s.operation, "collection.json")
	if err == nil || !strings.Contains(err.Error(), "before typed allocation") || got.Protected != nil {
		t.Fatal("oversized history array reached typed decode", err)
	}
}
