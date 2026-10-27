package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/rsyncengine"
	"golang.org/x/crypto/ssh"
)

// This helper is launched as a destination-owned process by the real ARM SSH
// lease fixture. It is test-only; the product sync worker has a separate
// reviewed authority/control protocol.
func TestARMSSHRsyncWorker(t *testing.T) {
	if os.Getenv("ARM_TEST_RSYNC_WORKER") != "1" {
		return
	}
	var role rsyncengine.Role
	switch os.Getenv("ARM_TEST_RSYNC_ROLE") {
	case "receive":
		role = rsyncengine.ServerReceiver
	case "send":
		role = rsyncengine.ServerSender
	default:
		fmt.Fprintln(os.Stderr, "invalid test worker role")
		os.Exit(71)
	}
	_, err := rsyncengine.Run(context.Background(), sshWorkerStdio{}, role, os.Getenv("ARM_TEST_RSYNC_ROOT"), rsyncengine.Metadata{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rsync worker:", err)
		os.Exit(72)
	}
	os.Exit(0) // keep the go test PASS line out of SSH stdout's rsync bytes
}

type sshWorkerStdio struct{}

func (sshWorkerStdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (sshWorkerStdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (sshWorkerStdio) Close() error                { return os.Stdin.Close() }

type sshRsyncChannel struct {
	read    io.Reader
	write   io.WriteCloser
	session *ssh.Session
}

func (c *sshRsyncChannel) Read(p []byte) (int, error)  { return c.read.Read(p) }
func (c *sshRsyncChannel) Write(p []byte) (int, error) { return c.write.Write(p) }
func (c *sshRsyncChannel) Close() error                { _ = c.write.Close(); return c.session.Close() }

func testShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func TestRsyncOverARMMeshSSHLeasedExecution(t *testing.T) {
	f := newPublicSSHFixture(t)
	source := filepath.Join(t.TempDir(), "source")
	remoteStage := filepath.Join(t.TempDir(), "remote-stage")
	localStage := filepath.Join(t.TempDir(), "local-stage")
	for _, dir := range []string{source, remoteStage, localStage} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	want := bytes.Repeat([]byte("ARM encrypted rsync stream\n"), 1<<15)
	if err := os.WriteFile(filepath.Join(source, "data"), want, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transfer := func(localRole rsyncengine.Role, localRoot, remoteRoot, workerRole string) rsyncengine.Stats {
		command := "env ARM_TEST_RSYNC_WORKER=1 ARM_TEST_RSYNC_ROLE=" + testShellQuote(workerRole) +
			" ARM_TEST_RSYNC_ROOT=" + testShellQuote(remoteRoot) + " " + testShellQuote(executable) +
			" -test.run=^TestARMSSHRsyncWorker$"
		q := f.control(command)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		client := f.dial(t, ctx, q)
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		stdin, err := session.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := session.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		session.Stderr = &stderr
		if err := session.Start(command); err != nil {
			t.Fatal(err)
		}
		stream := &sshRsyncChannel{read: stdout, write: stdin, session: session}
		stats, runErr := rsyncengine.Run(ctx, stream, localRole, localRoot, rsyncengine.Metadata{})
		_ = stdin.Close()
		waitErr := session.Wait()
		if runErr != nil || waitErr != nil {
			t.Fatalf("rsync over ARM SSH: client=%v worker=%v stderr=%s", runErr, waitErr, stderr.String())
		}
		status := f.status(t, q)
		if status.State != "admitted" || status.Execution == nil || status.Execution.Lease == nil || status.Execution.Lease.State != "terminal" || status.Execution.Lease.Outcome != "completed" || status.Fence == nil {
			t.Fatalf("missing exact terminal SSH lease/receipt: %+v", status)
		}
		return stats
	}
	first := transfer(rsyncengine.ClientSender, source, remoteStage, "receive")
	checkFile := func(path string) {
		got, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(got) != sha256.Sum256(want) {
			t.Fatalf("content hash mismatch at %s: %v", path, err)
		}
	}
	checkFile(filepath.Join(remoteStage, "data"))
	copy(want[32768:32778], []byte("new bytes!"))
	if err := os.WriteFile(filepath.Join(source, "data"), want, 0600); err != nil {
		t.Fatal(err)
	}
	delta := transfer(rsyncengine.ClientSender, source, remoteStage, "receive")
	checkFile(filepath.Join(remoteStage, "data"))
	if delta.Read+delta.Written >= (first.Read+first.Written)/2 {
		t.Fatalf("changed file missed rsync delta over ARM SSH: first=%d delta=%d", first.Read+first.Written, delta.Read+delta.Written)
	}
	t.Logf("ARM SSH first wire bytes=%d changed-file bytes=%d", first.Read+first.Written, delta.Read+delta.Written)
	transfer(rsyncengine.ClientReceiver, localStage, source, "send")
	checkFile(filepath.Join(localStage, "data"))
}
