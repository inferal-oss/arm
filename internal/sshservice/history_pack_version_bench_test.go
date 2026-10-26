package sshservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/historypack"
)

// This exercises the same native identity/version capture used immediately
// before each hot unlink, with a real published pack and unchanged evidence.
func BenchmarkSourceArchivePackVersionCapture(b *testing.B) {
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		b.Fatal(err)
	}
	root = filepath.Join(root, "packs")
	if err = os.Mkdir(root, 0700); err != nil {
		b.Fatal(err)
	}
	prepared, err := historypack.Prepare(b.Context(), root, strings.Repeat("a", 32), strings.Repeat("b", 32), []historypack.Record{{Key: historypack.Key{Domain: "ssh-source", Principal: "source/target", Value: "original-key"}, Payload: []byte("verified fixture bytes")}})
	if err != nil {
		b.Fatal(err)
	}
	receipt, err := historypack.Publish(b.Context(), root, prepared)
	if err != nil {
		b.Fatal(err)
	}
	expected, err := captureSourceArchivePackVersion(root, receipt)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		got, err := captureSourceArchivePackVersion(root, receipt)
		if err != nil || got != expected {
			b.Fatal("native evidence changed", got, err)
		}
	}
}
