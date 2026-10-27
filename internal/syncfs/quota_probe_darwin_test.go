package syncfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const apfsInfoFixture = `<?xml version="1.0"?><plist version="1.0"><dict>
<key>FilesystemName</key><string>APFS</string>
<key>MountPoint</key><string>/test/volume</string>
<key>WritableVolume</key><true/>
<key>DeviceIdentifier</key><string>disk12s1</string>
<key>VolumeUUID</key><string>1111-2222</string>
</dict></plist>`

const apfsListFixture = `<?xml version="1.0"?><plist version="1.0"><dict><key>Containers</key><array><dict>
<key>Volumes</key><array><dict>
<key>DeviceIdentifier</key><string>disk12s1</string>
<key>APFSVolumeUUID</key><string>1111-2222</string>
<key>CapacityQuota</key><integer>1048576</integer>
<key>CapacityInUse</key><integer>4096</integer>
</dict></array></dict></array></dict></plist>`

func TestAPFSQuotaPlistRequiresFiniteMatchingVolume(t *testing.T) {
	info, err := decodePlistDictionary([]byte(apfsInfoFixture))
	if err != nil {
		t.Fatal(err)
	}
	list, err := decodePlistDictionary([]byte(apfsListFixture))
	if err != nil {
		t.Fatal(err)
	}
	v, err := apfsQuotaFromPlists(info, list, "/test/volume")
	if err != nil || v.Scope() != "apfs-volume:1111-2222" || v.HardBytes() != 1048576 || v.UsedBytes() != 4096 {
		t.Fatalf("finite APFS volume evidence: %+v %v", v, err)
	}
	list, err = decodePlistDictionary([]byte(strings.Replace(apfsListFixture, "<integer>1048576</integer>", "<integer>0</integer>", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := apfsQuotaFromPlists(info, list, "/test/volume"); !errors.Is(err, ErrQuotaUnsupported) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("unquoted volume accepted: %+v %v", v, err)
	}
	list, err = decodePlistDictionary([]byte(strings.Replace(apfsListFixture, "1111-2222", "other-volume", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := apfsQuotaFromPlists(info, list, "/test/volume"); !errors.Is(err, ErrChanged) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("changed volume identity accepted: %+v %v", v, err)
	}
	if v, err := apfsQuotaFromPlists(info, list, "/test/other"); !errors.Is(err, ErrChanged) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("changed mount identity accepted: %+v %v", v, err)
	}
	list, err = decodePlistDictionary([]byte(strings.Replace(apfsListFixture, "<key>APFSVolumeUUID</key><string>1111-2222</string>", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := apfsQuotaFromPlists(info, list, "/test/volume"); !errors.Is(err, ErrQuotaUnknown) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("missing native UUID accepted: %+v %v", v, err)
	}
}

func TestAPFSQuotaPlistRejectsTruncation(t *testing.T) {
	if _, err := decodePlistDictionary([]byte(strings.TrimSuffix(apfsListFixture, "</plist>"))); !errors.Is(err, ErrQuotaUnknown) {
		t.Fatalf("truncated plist accepted: %v", err)
	}
}

func TestAPFSQuotaPlistRejectsTooManyDictionaryEntries(t *testing.T) {
	var document strings.Builder
	document.WriteString(`<plist version="1.0"><dict>`)
	for i := range 257 {
		fmt.Fprintf(&document, `<key>k%d</key><string>v</string>`, i)
	}
	document.WriteString(`</dict></plist>`)
	if _, err := decodePlistDictionary([]byte(document.String())); !errors.Is(err, ErrQuotaUnknown) {
		t.Fatalf("257 dictionary entries accepted: %v", err)
	}
}

func TestAPFSQuotaPlistRequiresOnlyStructuredContent(t *testing.T) {
	valid := `<?xml version="1.0"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + apfsListFixture[strings.Index(apfsListFixture, "<plist"):]
	if _, err := decodePlistDictionary([]byte(valid)); err != nil {
		t.Fatalf("normal plist prolog rejected: %v", err)
	}
	bad := map[string]string{
		"missing plist version": strings.Replace(apfsListFixture, `<plist version="1.0">`, `<plist>`, 1),
		"new plist version":     strings.Replace(apfsListFixture, `<plist version="1.0">`, `<plist version="2.0">`, 1),
		"duplicate version":     strings.Replace(apfsListFixture, `<plist version="1.0">`, `<plist version="1.0" version="2.0">`, 1),
		"extra plist attribute": strings.Replace(apfsListFixture, `<plist version="1.0">`, `<plist version="1.0" future="yes">`, 1),
		"integer unit":          strings.Replace(apfsListFixture, `<integer>1048576</integer>`, `<integer unit="KiB">1048576</integer>`, 1),
		"boolean attribute":     strings.Replace(apfsInfoFixture, `<true/>`, `<true future="yes"/>`, 1),
		"dict attribute":        strings.Replace(apfsListFixture, `<dict>`, `<dict future="yes">`, 1),
		"array attribute":       strings.Replace(apfsListFixture, `<array>`, `<array future="yes">`, 1),
		"string attribute":      strings.Replace(apfsInfoFixture, `<string>APFS</string>`, `<string future="yes">APFS</string>`, 1),
		"key attribute":         strings.Replace(apfsListFixture, `<key>CapacityQuota</key>`, `<key future="yes">CapacityQuota</key>`, 1),
		"before root":           "unexpected" + apfsListFixture,
		"inside root":           strings.Replace(apfsListFixture, `<plist version="1.0">`, `<plist version="1.0">unexpected`, 1),
		"inside dict":           strings.Replace(apfsListFixture, `<dict>`, `<dict>unexpected`, 1),
		"inside array":          strings.Replace(apfsListFixture, `<array>`, `<array>unexpected`, 1),
		"boolean text":          strings.Replace(apfsInfoFixture, `<true/>`, `<true>unexpected</true>`, 1),
		"boolean child":         strings.Replace(apfsInfoFixture, `<true/>`, `<true><string>unexpected</string></true>`, 1),
		"nested integer":        strings.Replace(apfsListFixture, `<integer>1048576</integer>`, `<integer>1048576<string>unexpected</string></integer>`, 1),
		"nested string":         strings.Replace(apfsInfoFixture, `<string>APFS</string>`, `<string>AP<string>unexpected</string>FS</string>`, 1),
		"container comment":     strings.Replace(apfsListFixture, `<array>`, `<array><!--unexpected-->`, 1),
	}
	for name, raw := range bad {
		t.Run(name, func(t *testing.T) {
			if got, err := decodePlistDictionary([]byte(raw)); !errors.Is(err, ErrQuotaUnknown) || got != nil {
				t.Fatalf("malformed plist produced evidence: %+v %v", got, err)
			}
		})
	}
}

func TestDiskutilPipeLoanHelper(t *testing.T) {
	if os.Getenv("ARM_SYNCFS_TEST_PIPE_LOAN") != "1" {
		return
	}
	// ExtraFiles gives this direct child descriptor 3. Loan its stdout pipe
	// writer to the original test process, then exit without spawning anyone.
	if _, err := unix.SendmsgN(3, []byte{1}, unix.UnixRights(1), nil, 0); err != nil {
		t.Fatal(err)
	}
}

func TestDiskutilPlistCommandBoundsInheritedPipeWait(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Close(pair[0]); err != nil {
			t.Errorf("parent loan socket was not closed: %v", err)
		}
	})
	childSocket := os.NewFile(uintptr(pair[1]), "quota-pipe-loan")
	t.Cleanup(func() {
		if err := childSocket.Close(); err != nil {
			t.Errorf("child loan socket was not closed: %v", err)
		}
	})
	t.Setenv("ARM_SYNCFS_TEST_PIPE_LOAN", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = diskutilPlistCommandWithFiles(context.Background(), []*os.File{childSocket}, executable, "-test.run=^TestDiskutilPipeLoanHelper$")
	if !errors.Is(err, ErrQuotaUnknown) {
		t.Fatalf("loaned pipe did not make quota evidence unknown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("loaned pipe kept query waiting for %s", elapsed)
	}
	var payload [1]byte
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := unix.Recvmsg(pair[0], payload[:], oob, unix.MSG_DONTWAIT)
	if err != nil || n != 1 || payload[0] != 1 {
		t.Fatalf("original child did not loan stdout: bytes=%d err=%v", n, err)
	}
	messages, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(messages) != 1 {
		t.Fatalf("stdout descriptor loan was malformed: %v", err)
	}
	loaned, err := unix.ParseUnixRights(&messages[0])
	if err != nil {
		t.Fatalf("stdout descriptor rights were malformed: %v", err)
	}
	defer func() {
		for _, fd := range loaned {
			unix.Close(fd)
		}
	}()
	if len(loaned) != 1 {
		t.Fatalf("expected one loaned stdout descriptor, got %d", len(loaned))
	}
	if err := unix.Close(loaned[0]); err != nil {
		t.Fatalf("loaned stdout was not closed: %v", err)
	}
	loaned = nil
}
