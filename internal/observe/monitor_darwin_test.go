package observe

import (
	"testing"
	"time"
)

func TestDarwinMonitorDiskUsesStableDriverIdentityAndStrictCounters(t *testing.T) {
	data := []byte(`<plist><array><dict><key>IORegistryEntryID</key><integer>123</integer><key>IORegistryEntryName</key><string>disk driver</string><key>Statistics</key><dict><key>Bytes (Read)</key><integer>9007199254740993</integer><key>Bytes (Write)</key><integer>512</integer></dict></dict></array></plist>`)
	rows, e := darwinMonitorDisk(data, time.Now())
	if e != nil || len(rows) != 1 || rows[0].id != "darwin-block-driver:123" || rows[0].read != 9007199254740993 {
		t.Fatal("lost identity/exact counter", rows, e)
	}
	for _, data := range []string{`<plist><dict/></plist>`, `<plist><array><dict><key>IORegistryEntryID</key><integer>123</integer></dict></array></plist>`, `<plist><array><dict><key>Statistics</key><dict/></dict></array></plist>`} {
		if _, e := darwinMonitorDisk([]byte(data), time.Now()); e == nil {
			t.Fatal("incomplete disk counters were zero")
		}
	}
}
