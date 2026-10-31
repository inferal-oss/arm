package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
	"golang.org/x/sys/unix"
)

// This test-only observation runs only after the original Close has returned
// an error, while the same test process is still alive. It grants no cleanup
// authority and never reports raw host paths or process identifiers.
type forwardUnixCloseActivity struct {
	StorageState           string
	ArtifactPhase          string
	FailureClass           string
	FailureProcessMatch    string
	IdentityBefore         string
	IdentityAfter          string
	LsofOutcome            string
	OwnProcessDescriptors  []forwardUnixOpenDescriptor
	DescriptorLimitReached bool
	Truncated              bool
}

type forwardUnixOpenDescriptor struct {
	FDClass      string
	Type         string
	ArtifactPath string
}

type forwardUnixLimitedCapture struct {
	bytes.Buffer
	truncated bool
}

func (b *forwardUnixLimitedCapture) Write(p []byte) (int, error) {
	const limit = 64 << 10
	room := limit - b.Len()
	if len(p) > room {
		if room > 0 {
			_, _ = b.Buffer.Write(p[:room])
		}
		b.truncated = true
		return room, errors.New("diagnostic stream limit reached")
	}
	return b.Buffer.Write(p)
}

func inspectForwardUnixCloseActivity(parent context.Context, m *manager.Manager, sourceID, storageID string) forwardUnixCloseActivity {
	result := forwardUnixCloseActivity{LsofOutcome: "not_run"}
	if runtime.GOOS != "darwin" || parent == nil || m == nil || sourceID == "" || storageID == "" {
		return result
	}
	lease, err := m.Inspect(storageID)
	if err != nil || lease.Request.Kind != "storage" || lease.Request.Reason != "Forward Unix source directory for "+sourceID || lease.Artifact == nil || lease.Artifact.Identity == nil || lease.Artifact.Name != storageID {
		result.LsofOutcome = "original_storage_identity_unavailable"
		return result
	}
	result.StorageState = lease.State
	result.ArtifactPhase = lease.Artifact.Phase
	result.FailureClass, result.FailureProcessMatch = forwardUnixDiagnosticFailureClassAndProcessMatch(lease.Failure, os.Getpid())
	path := lease.Artifact.Path
	if !forwardUnixDiagnosticArtifactPath(lease.Artifact.Phase, path, lease.ManagedDir, storageID) {
		result.LsofOutcome = "artifact_path_unavailable"
		return result
	}
	result.IdentityBefore = forwardUnixDiagnosticIdentity(path, *lease.Artifact.Identity)
	if result.IdentityBefore != "matched" {
		result.LsofOutcome = "artifact_identity_unavailable"
		return result
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	// The -p filter observes this original test process only. CommandContext
	// owns and joins its lsof child; it never signals the observed process.
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(os.Getpid()), "+D", path, "-F", "pftn")
	cmd.WaitDelay = 250 * time.Millisecond
	var stdout, stderr forwardUnixLimitedCapture
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	result.Truncated = stdout.truncated || stderr.truncated
	result.IdentityAfter = forwardUnixDiagnosticIdentity(path, *lease.Artifact.Identity)
	if ctx.Err() != nil {
		result.LsofOutcome = "deadline"
	} else if result.Truncated {
		result.LsofOutcome = "output_limit"
	} else if stderr.Len() > 0 {
		result.LsofOutcome = "warning"
	} else if runErr == nil {
		result.LsofOutcome = "completed"
	} else {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) && exit.ExitCode() == 1 {
			result.LsofOutcome = "no_match"
		} else {
			result.LsofOutcome = "failed"
		}
	}
	if !result.Truncated && result.IdentityAfter == "matched" {
		result.OwnProcessDescriptors, result.DescriptorLimitReached = forwardUnixParseOwnDescriptors(stdout.String(), strconv.Itoa(os.Getpid()), path)
	}
	return result
}

func forwardUnixDiagnosticArtifactPath(phase, path, managedDir, storageID string) bool {
	if storageID == "" || managedDir == "" {
		return false
	}
	switch phase {
	case "owned":
		return path == managedDir
	case "quarantined":
		return path == filepath.Join(filepath.Dir(managedDir), ".reclaim-"+storageID)
	default:
		return false
	}
}

func forwardUnixDiagnosticFailureClassAndProcessMatch(failure string, ownPID int) (string, string) {
	var class, prefix string
	switch {
	case strings.HasPrefix(failure, "active open files or working directories: ["):
		class, prefix = "preflight-active", "active open files or working directories: ["
	case strings.HasPrefix(failure, "artifact is still open by process(es) ["):
		class, prefix = "cleanup-active", "artifact is still open by process(es) ["
	default:
		return "unknown", "unknown"
	}
	if !strings.HasSuffix(failure, "]") {
		return class, "unknown"
	}
	listed := strings.TrimSuffix(strings.TrimPrefix(failure, prefix), "]")
	parts := strings.FieldsFunc(listed, func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) == 0 {
		return class, "unknown"
	}
	matched := false
	for _, part := range parts {
		pid, err := strconv.Atoi(part)
		if err != nil || pid <= 0 {
			return class, "unknown"
		}
		matched = matched || pid == ownPID
	}
	if matched {
		return class, "includes_original_process"
	}
	return class, "other_processes_only"
}

func forwardUnixDiagnosticIdentity(path string, expected model.FileIdentity) string {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "unavailable"
	}
	defer unix.Close(fd)
	got, err := storage.DirectoryIdentity(fd)
	if err != nil {
		return "unavailable"
	}
	if got != expected {
		return "mismatch"
	}
	return "matched"
}

func forwardUnixParseOwnDescriptors(raw, ownPID, artifact string) ([]forwardUnixOpenDescriptor, bool) {
	var result []forwardUnixOpenDescriptor
	limitReached := false
	var pid, fd, typ, name string
	flush := func() {
		if pid != ownPID || fd == "" {
			return
		}
		if len(result) >= 64 {
			limitReached = true
			return
		}
		class := "other"
		if strings.HasPrefix(fd, "cwd") {
			class = "cwd"
		} else if fd[0] >= '0' && fd[0] <= '9' {
			class = "numbered"
		}
		if len(typ) > 16 || strings.IndexFunc(typ, func(r rune) bool { return r < 'A' || r > 'z' }) >= 0 {
			typ = "other"
		}
		result = append(result, forwardUnixOpenDescriptor{FDClass: class, Type: typ, ArtifactPath: forwardUnixArtifactRelativePath(name, artifact)})
	}
	for _, line := range strings.Split(raw, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			flush()
			pid, fd, typ, name = line[1:], "", "", ""
		case 'f':
			flush()
			fd, typ, name = line[1:], "", ""
		case 't':
			typ = line[1:]
		case 'n':
			name = line[1:]
		}
	}
	flush()
	return result, limitReached
}

func forwardUnixArtifactRelativePath(name, artifact string) string {
	if name == "" {
		return "<unavailable>"
	}
	rel, err := filepath.Rel(artifact, name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "<outside-artifact>"
	}
	if rel == "." {
		return "<artifact>"
	}
	return "<artifact>/" + filepath.ToSlash(rel)
}

func TestForwardUnixCloseDiagnosticParser(t *testing.T) {
	const artifact = "/private-artifact"
	raw := "p42\nf7u\ntREG\nn/private-artifact/nested/socket\nfcwd\ntDIR\nn/private-artifact\nf8u\ntREG\nn/elsewhere/file\np43\nf9u\ntREG\nn/private-artifact/other\n"
	got, limited := forwardUnixParseOwnDescriptors(raw, "42", artifact)
	if limited || len(got) != 3 {
		t.Fatalf("original process descriptor selection: %+v limited=%v", got, limited)
	}
	want := []forwardUnixOpenDescriptor{
		{FDClass: "numbered", Type: "REG", ArtifactPath: "<artifact>/nested/socket"},
		{FDClass: "cwd", Type: "DIR", ArtifactPath: "<artifact>"},
		{FDClass: "numbered", Type: "REG", ArtifactPath: "<outside-artifact>"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("descriptor %d: got %+v want %+v", i, got[i], want[i])
		}
	}
	if got, limited := forwardUnixParseOwnDescriptors(raw, "43", artifact); limited || len(got) != 1 || got[0].ArtifactPath != "<artifact>/other" {
		t.Fatalf("other process selection: %+v limited=%v", got, limited)
	}
}

func TestForwardUnixCloseDiagnosticArtifactPathAndFailureClass(t *testing.T) {
	const managedDir, storageID = "/private-artifact/storage/id", "id"
	for _, tc := range []struct {
		phase, path string
		want        bool
	}{
		{"owned", managedDir, true},
		{"quarantined", "/private-artifact/storage/.reclaim-id", true},
		{"owned", "/private-artifact/storage/.reclaim-id", false},
		{"quarantined", managedDir, false},
		{"removed", managedDir, false},
		{"owned", "/elsewhere/id", false},
	} {
		if got := forwardUnixDiagnosticArtifactPath(tc.phase, tc.path, managedDir, storageID); got != tc.want {
			t.Fatalf("path classification phase=%s: got %v want %v", tc.phase, got, tc.want)
		}
	}
	for _, tc := range []struct {
		failure, class, match string
	}{
		{"active open files or working directories: [42, 43]", "preflight-active", "includes_original_process"},
		{"active open files or working directories: [43]", "preflight-active", "other_processes_only"},
		{"artifact is still open by process(es) [42]", "cleanup-active", "includes_original_process"},
		{"artifact is still open by process(es) [43]", "cleanup-active", "other_processes_only"},
		{"artifact is still open by process(es) [bad]", "cleanup-active", "unknown"},
		{"unrelated refusal", "unknown", "unknown"},
	} {
		class, match := forwardUnixDiagnosticFailureClassAndProcessMatch(tc.failure, 42)
		if class != tc.class || match != tc.match {
			t.Fatalf("failure classification: got %s/%s want %s/%s", class, match, tc.class, tc.match)
		}
	}
}

func TestForwardUnixCloseDiagnosticOutputBound(t *testing.T) {
	for _, label := range []string{"stdout", "stderr"} {
		t.Run(label, func(t *testing.T) {
			var capture forwardUnixLimitedCapture
			const limit = 64 << 10
			n, err := capture.Write(bytes.Repeat([]byte{'x'}, limit+1))
			if n != limit || err == nil || !capture.truncated || capture.Len() != limit {
				t.Fatalf("capture limit: n=%d err=%v truncated=%v length=%d", n, err, capture.truncated, capture.Len())
			}
		})
	}
}
