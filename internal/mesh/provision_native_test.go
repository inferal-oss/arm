package mesh

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/meshssh"
)

func TestProvisionNativeChild(t *testing.T) {
	if os.Getenv("ARM_PROVISION_TEST_CHILD") != "1" {
		return
	}
	raw, e := base64.RawStdEncoding.DecodeString(os.Args[len(os.Args)-1])
	if e != nil {
		os.Exit(4)
	}
	var q meshssh.Transfer
	if e = json.Unmarshal(raw, &q); e != nil {
		os.Exit(5)
	}
	r, e := FinalizeProvision(q)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(6)
	}
	json.NewEncoder(os.Stdout).Encode(r)
	os.Exit(0)
}
func TestProvisionPublicationAndOneShotStartEvidence(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	source, e := os.Open(self)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	h := sha256.New()
	size, e := io.Copy(h, source)
	if e != nil {
		t.Fatal(e)
	}
	source.Seek(0, 0)
	q := meshssh.Transfer{Root: root, Key: strings.Repeat("a", 64), SHA256: hex.EncodeToString(h.Sum(nil)), Size: size, ExpectedUID: strconv.Itoa(os.Geteuid()), OS: runtime.GOOS, Arch: runtime.GOARCH, Version: buildinfo.Version, Protocol: LifecycleProtocol, Reason: "native publication fixture"}
	stage, final, _ := provisionPaths(q)
	if e = os.Mkdir(stage, 0700); e != nil {
		t.Fatal(e)
	}
	destination, e := os.OpenFile(filepath.Join(stage, "arm"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.Copy(destination, source); e != nil {
		t.Fatal(e)
	}
	destination.Close()
	raw, _ := json.Marshal(q)
	cmd := exec.Command(filepath.Join(stage, "arm"), "-test.run=^TestProvisionNativeChild$", "--", base64.RawStdEncoding.EncodeToString(raw))
	cmd.Env = append(os.Environ(), "ARM_PROVISION_TEST_CHILD=1")
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("finalizer failed: %v: %s", e, output)
	}
	var r ProvisionReceipt
	if e = json.Unmarshal(output, &r); e != nil {
		t.Fatal(e)
	}
	if r.Phase != "published_verified" || r.Binary != filepath.Join(final, "arm") {
		t.Fatal(r)
	}
	if _, e = os.Stat(filepath.Join(stage, "arm")); !os.IsNotExist(e) {
		t.Fatal("stage executable not removed after durable no-replace publication")
	}
	if _, e = FinalizeProvision(q); e == nil {
		t.Fatal("repeated mutation was allowed instead of read-only lookup")
	}
	if _, e = LookupProvision(q); e != nil {
		t.Fatal(e)
	}
	startup := DefaultStartup()
	home := filepath.Join(root, "state")
	if r, e = BeginProvisionStart(q, home, startup); e != nil || r.Phase != "start_intent" {
		t.Fatal(r, e)
	}
	if _, e = BeginProvisionStart(q, home, startup); e == nil {
		t.Fatal("start intent was automatically resent")
	}
	proof := RuntimeProof{Host: strings.Repeat("b", 32), UID: q.ExpectedUID, SHA256: q.SHA256, Size: q.Size, Version: q.Version, Protocol: q.Protocol, Method: "startup-open-file-snapshot", Captured: time.Now().UTC()}
	if _, e = FinishProvisionStart(q, proof, home+"-changed", startup); e == nil {
		t.Fatal("changed start home accepted")
	}
	if r, e = FinishProvisionStart(q, proof, home, startup); e != nil || r.Phase != "started_verified" {
		t.Fatal(r, e)
	}
	// A fresh reviewed intent may reuse identical immutable bytes, never replace
	// an existing installed inode or inherit a different artifact by version label.
	reused := q
	reused.Key = strings.Repeat("c", 64)
	reusedStage, _, _ := provisionPaths(reused)
	if e = os.Mkdir(reusedStage, 0700); e != nil {
		t.Fatal(e)
	}
	source.Seek(0, 0)
	copy, e := os.OpenFile(filepath.Join(reusedStage, "arm"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.Copy(copy, source); e != nil {
		t.Fatal(e)
	}
	copy.Close()
	beforeFinal, e := os.Stat(r.Binary)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(reused)
	reuseCmd := exec.Command(filepath.Join(reusedStage, "arm"), "-test.run=^TestProvisionNativeChild$", "--", base64.RawStdEncoding.EncodeToString(encoded))
	reuseCmd.Env = append(os.Environ(), "ARM_PROVISION_TEST_CHILD=1")
	reply, e := reuseCmd.CombinedOutput()
	if e != nil {
		t.Fatalf("exact binary reuse failed: %v: %s", e, reply)
	}
	var reuseReceipt ProvisionReceipt
	if e = json.Unmarshal(reply, &reuseReceipt); e != nil || !reuseReceipt.ReusedBinary {
		t.Fatal(reuseReceipt, e)
	}
	afterFinal, e := os.Stat(r.Binary)
	if e != nil || !os.SameFile(beforeFinal, afterFinal) {
		t.Fatal("binary reuse replaced immutable installed inode", e)
	}
	f, e := os.OpenFile(r.Binary, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt([]byte("changed!"), 0); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = LookupProvision(q); e == nil {
		t.Fatal("changed final bytes accepted based only on receipt")
	}
}
func TestProvisionIntentAndArtifactMetadataFailClosed(t *testing.T) {
	base := Artifact{Schema: 1, SHA256: strings.Repeat("a", 64), Size: 1, OS: "linux", Arch: "arm64", Version: buildinfo.Version, Protocol: LifecycleProtocol, Reason: "purpose"}
	if e := base.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Artifact){func(a *Artifact) { a.SHA256 = "../elsewhere" }, func(a *Artifact) { a.Size = MaxArtifactBytes + 1 }, func(a *Artifact) { a.OS = "windows" }, func(a *Artifact) { a.Version = "unverified" }, func(a *Artifact) { a.Reason = " \n " }} {
		a := base
		mutate(&a)
		if e := a.Validate(); e == nil {
			t.Fatal("invalid artifact accepted", a)
		}
	}
	// Catalog reads never create an absent ARM home.
	home := filepath.Join(t.TempDir(), "absent")
	items, e := Artifacts(home)
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	if _, e = os.Lstat(home); !os.IsNotExist(e) {
		t.Fatal("read-only catalog discovery wrote ARM state")
	}
}
