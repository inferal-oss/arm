package syncfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testEnrollmentRecord() EnrollmentRecord {
	return EnrollmentRecord{
		Version: "arm-sync-enrollment-v1", Provider: "xfs-project-quota",
		MountID: "test-mount", ScopeID: "test-scope", ProjectID: 42,
		ProjectRootID: "test-project", SelectedRootID: "test-selected",
		HomeRootID: "test-home", BackingRootID: "test-backing",
		OwnerUID: strconv.Itoa(os.Geteuid()), OwnerEpoch: 1, ChargedHardBytes: 64 << 20,
	}
}

// These positive results are only strict record decoding, never native
// enrollment or disk admission evidence.
func TestEnrollmentRecordCanonicalReadOnlyCodec(t *testing.T) {
	record := testEnrollmentRecord()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEnrollmentRecord(raw)
	if err != nil || got != record {
		t.Fatalf("canonical record decode: %+v %v", got, err)
	}
	for name, input := range map[string][]byte{
		"noncanonical whitespace": append(append([]byte{}, raw...), '\n'),
		"duplicate field":         bytes.Replace(raw, []byte(`"version":`), []byte(`"version":"alias","version":`), 1),
		"unknown field":           bytes.Replace(raw, []byte(`"version":`), []byte(`"extra":1,"version":`), 1),
		"oversized":               []byte(strings.Repeat("x", maxEnrollmentRecordBytes+1)),
		"wrong provider":          bytes.Replace(raw, []byte(`"xfs-project-quota"`), []byte(`"unverified"`), 1),
		"zero project":            bytes.Replace(raw, []byte(`"project_id":42`), []byte(`"project_id":0`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeEnrollmentRecord(input); !errors.Is(err, ErrEnrollmentRecord) {
				t.Fatalf("malformed record accepted: %v", err)
			}
		})
	}
}

func TestEnrollmentRecordRequiresHeldNoFollowPrivateFile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	record := testEnrollmentRecord()
	record.BackingRootID, err = root.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(record)
	path := filepath.Join(base, EnrollmentRecordName)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnrollmentRecord(root)
	if err != nil || got.Record() != record || len(got.Digest()) != 64 {
		t.Fatalf("held record unavailable: %+v %v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnrollmentRecord(root); !errors.Is(err, ErrEnrollmentRecord) {
		t.Fatalf("public record mode accepted: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "target"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnrollmentRecord(root); !errors.Is(err, ErrEnrollmentRecord) {
		t.Fatalf("symlink record accepted: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(base, "target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnrollmentRecord(root); !errors.Is(err, ErrEnrollmentRecord) {
		t.Fatalf("multiply linked record accepted: %v", err)
	}
}

func TestBackingHeadroomRefusesUnenrolledOrdinaryFilesystem(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(base, "project")
	if err := os.Mkdir(projectPath, 0700); err != nil {
		t.Fatal(err)
	}
	backing, err := OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer backing.Close()
	project, err := OpenRoot(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	if _, err := ProbeBackingHeadroom(context.Background(), backing, project, &RecordedEnrollment{}); !errors.Is(err, ErrBackingHeadroom) {
		t.Fatalf("caller-made record accepted: %v", err)
	}
	record := testEnrollmentRecord()
	record.BackingRootID, err = backing.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	record.ProjectRootID, err = project.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(base, EnrollmentRecordName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	enrollment, err := ReadEnrollmentRecord(backing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeBackingHeadroom(context.Background(), backing, project, enrollment); !errors.Is(err, ErrBackingHeadroom) {
		t.Fatalf("unbound backing anchor accepted: %v", err)
	}
}
