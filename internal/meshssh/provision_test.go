package meshssh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProvisionFixedScriptRequiresApprovalBeforeAnyWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	cmd := exec.Command("/bin/sh", "-c", uploadScript, "test", root, strings.Repeat("a", 64), strings.Repeat("b", 64), "10", strconv.Itoa(os.Geteuid()), "unused", "linux")
	cmd.Stdin = strings.NewReader("NOT_APPROVED\nsecret bytes")
	if e := cmd.Run(); e == nil {
		t.Fatal("script accepted absent approval")
	}
	if _, e := os.Lstat(root); !os.IsNotExist(e) {
		t.Fatal("unapproved script wrote installation root")
	}
}
func TestProvisionFixedScriptChecksDigestBeforeExecuting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-root")
	stage := filepath.Join(root, "stage-"+strings.Repeat("a", 64), "arm")
	payload := []byte("#!/bin/sh\nexit 42\n")
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) == strings.Repeat("b", 64) {
		t.Fatal("fixture")
	}
	// Linux shell uses an absolute Linux stat; skip this script-platform fixture
	// on other hosts. Native finalizer/transport gates remain separate tests.
	if _, e := os.Stat("/usr/bin/sha256sum"); e != nil {
		t.Skip("Linux fixed hashing fixture")
	}
	cmd := exec.Command("/bin/sh", "-c", uploadScript, "test", root, strings.Repeat("a", 64), strings.Repeat("b", 64), strconv.Itoa(len(payload)), strconv.Itoa(os.Geteuid()), "unused", "linux")
	cmd.Stdin = ioReader("ARM_REVIEWED_PROVISION_V1\n", payload)
	e := cmd.Run()
	if e == nil {
		t.Fatal("script accepted wrong artifact digest")
	}
	if exit, ok := e.(*exec.ExitError); ok && exit.ExitCode() == 42 {
		t.Fatal("executed artifact before matching reviewed digest")
	}
	if raw, e := os.ReadFile(stage); e != nil || !bytes.Equal(raw, payload) {
		t.Fatal("partial stage evidence missing", e)
	}
}
func ioReader(prefix string, payload []byte) *bytes.Reader {
	return bytes.NewReader(append([]byte(prefix), payload...))
}
func TestProvisionTransferInputsClosedAndShellQuoted(t *testing.T) {
	q := Transfer{Root: "/tmp/private-root", Key: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), Size: 1, ExpectedUID: "501", OS: "linux", Arch: "arm64", Version: "test", Protocol: 1, Reason: "purpose"}
	if e := q.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"/tmp/../elsewhere", "relative", "/tmp/$(whoami)", "/tmp/a\ncommand", "/tmp/a;exit"} {
		copy := q
		copy.Root = bad
		if e := copy.Validate(); e == nil {
			t.Fatal("accepted unsafe exact path", bad)
		}
	}
	for _, bad := range []string{"-oProxyCommand=x", "host name", "host\nname", ""} {
		if validDestination(bad) {
			t.Fatal("accepted invalid SSH destination")
		}
	}
	for _, value := range []string{"simple", "space value", "a'b", "$(echo should-not-run); \n \""} {
		cmd := exec.Command("/bin/sh", "-c", "printf '%s' "+quote(value))
		raw, e := cmd.Output()
		if e != nil || string(raw) != value {
			t.Fatal("shell quote changed literal data", value, e)
		}
	}
}

func TestProvisionStateHomeBoundsAndOrdinaryPlatformSpaces(t *testing.T) {
	for _, good := range []string{"/private/tmp/state", "/Users/person/Library/Application Support/arm"} {
		if !ValidStateHome(good) {
			t.Fatal("ordinary home refused", good)
		}
	}
	for _, bad := range []string{"", "relative", "/tmp/../state", "/tmp/a\nstate", "/tmp/a\x00state", "/" + strings.Repeat("a", 4096)} {
		if ValidStateHome(bad) {
			t.Fatal("unsafe home accepted", bad)
		}
	}
}
