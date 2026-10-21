package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGradleJDKSelectionPinsCanonicalInstalledHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "installed jdk")
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	java := filepath.Join(home, "bin", "java")
	if err := os.WriteFile(java, []byte("fixture: validation must not execute Java"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "jdk alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JAVA_HOME", alias)
	got, err := gradleJavaHome()
	want, _ := filepath.EvalSymlinks(home)
	if err != nil || got != want {
		t.Fatal("JDK selection did not pin the canonical configured home", got, want, err)
	}
	if err := os.Chmod(java, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := gradleJavaHome(); err == nil {
		t.Fatal("nonexecutable Java accepted")
	}
	for _, path := range []string{"", "relative/jdk", filepath.Join(t.TempDir(), "absent")} {
		if _, err := canonicalGradleJavaHome(path); err == nil {
			t.Fatal("invalid JDK home accepted", path)
		}
	}
}

func TestGradleJVMBoundsAndCompatibleMinimumHeap(t *testing.T) {
	args, heap, metaspace, err := gradleJVMArgs(1 << 30)
	if err != nil || heap != 512<<20 || metaspace != 256<<20 || !strings.Contains(strings.Join(args, " "), "-Xms64m") {
		t.Fatal("wrong managed JVM policy", args, heap, metaspace, err)
	}
	if _, _, _, err := gradleJVMArgs(127 << 20); err == nil {
		t.Fatal("insufficient client budget accepted")
	}
	var out gradleBoundedText
	if _, err := out.Write([]byte(strings.Repeat("x", 16<<10))); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("x")); err == nil || !out.overflow || len(out.text) != 16<<10 {
		t.Fatal("unbounded OS inventory output accepted")
	}
}

func TestGradleStartupJVMBudgetsDoNotPromiseTheSameMemoryTwice(t *testing.T) {
	_, probeHeap, probeMeta, err := gradleJVMArgs(gradleProbeMemory)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []int64{512 << 20, 1 << 30, 4 << 30} {
		_, heap, meta, err := gradleServiceJVMArgs(claim)
		if err != nil || heap+meta+probeHeap+probeMeta >= claim {
			t.Fatalf("configured JVM limits exceed shared startup promise: claim=%d daemon=%d probe=%d err=%v", claim, heap+meta, probeHeap+probeMeta, err)
		}
	}
	if _, _, _, err := gradleServiceJVMArgs((512 << 20) - 1); err == nil {
		t.Fatal("service startup allowance omitted")
	}
}
