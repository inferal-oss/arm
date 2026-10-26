package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForwardStreamTokenFileSecurity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "token")
	token := strings.Repeat("a", 64)
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readForwardToken(path); err != nil || got != token {
		t.Fatalf("private token refused: %v", err)
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(link); err == nil {
		t.Fatal("symlink token accepted")
	}
	hard := filepath.Join(root, "hardlink")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(path); err == nil {
		t.Fatal("multiply linked token accepted")
	}
	if err := os.Remove(hard); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(path); err == nil {
		t.Fatal("world-readable token accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(token+"\nextra"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readForwardToken(path); err == nil {
		t.Fatal("token with trailing data accepted")
	}
}
