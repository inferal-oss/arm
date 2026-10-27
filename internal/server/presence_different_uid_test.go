package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/presence"
	"getarm.org/internal/server"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type uidPeerReply struct {
	Error             string `json:"error,omitempty"`
	UID, GID          int
	Host, Record, URL string
	Held, Queued      model.Lease
	Report            *model.PeerOverview `json:"report,omitempty"`
	Stopped           bool
}

// This helper is only launched as an original child by the explicitly opted-in
// root fixture below. Each child uses its own private API; neither reads another
// user's ARM_HOME, token or database. Quantities are cooperative memory promises,
// not native CPU/physical-isolation acceptance.
func TestPeerDifferentUIDHelperProcess(t *testing.T) {
	if os.Getenv("ARM_PEER_UID_HELPER") != "1" {
		return
	}
	fail := func(err error) { _ = json.NewEncoder(os.Stdout).Encode(uidPeerReply{Error: err.Error()}); os.Exit(2) }
	uid, _ := strconv.Atoi(os.Getenv("ARM_PEER_EXPECT_UID"))
	gid, _ := strconv.Atoi(os.Getenv("ARM_PEER_EXPECT_GID"))
	groups, err := os.Getgroups()
	if err != nil {
		fail(err)
	}
	if os.Geteuid() != uid || os.Getegid() != gid || len(groups) != 0 {
		fail(fmt.Errorf("unexpected native child credentials %d/%d groups=%v", os.Geteuid(), os.Getegid(), groups))
	}
	home := os.Getenv("ARM_PEER_SERVE_HOME")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	}()
	var ep local.Endpoint
	for until := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		ep, err = local.ReadEndpoint(home)
		if err == nil {
			break
		}
		if time.Now().After(until) {
			cancel()
			<-done
			fail(err)
		}
	}
	call := func(method string, in, out any) error {
		c, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		return client.Call(c, home, method, in, out)
	}
	var held, queued model.Lease
	if err = call("acquire", model.Request{Reason: "private child held purpose", Resources: model.Resources{Memory: 700}, TTL: 2 * time.Minute}, &held); err != nil {
		cancel()
		<-done
		fail(err)
	}
	if err = call("acquire", model.Request{Reason: "private child queued purpose", Resources: model.Resources{Memory: 600}, TTL: time.Minute, Queue: true}, &queued); err != nil {
		cancel()
		<-done
		fail(err)
	}
	d, err := presence.Open()
	if err != nil {
		cancel()
		<-done
		fail(err)
	}
	own := uidPeerReply{UID: uid, GID: gid, Held: held, Queued: queued, URL: strings.Replace(ep.URL, "/v1/ws", "/v1/peers", 1)}
	// Host is taken from our public native-owned record, avoiding full Status work.
	for until := time.Now().Add(6 * time.Second); own.Record == ""; time.Sleep(20 * time.Millisecond) {
		for _, candidate := range d.Scan().Candidates {
			if candidate.UID == uint32(uid) && candidate.Record.URL == own.URL {
				own.Host = candidate.Record.Host
				own.Record = candidate.Name
			}
		}
		if time.Now().After(until) {
			cancel()
			<-done
			_ = d.Close()
			fail(fmt.Errorf("own publication unavailable"))
		}
	}
	_ = d.Close()
	enc := json.NewEncoder(os.Stdout)
	if err = enc.Encode(own); err != nil {
		cancel()
		<-done
		fail(err)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		command := scanner.Text()
		if command == "sample" {
			var report model.PeerOverview
			if err = call("peers_status", nil, &report); err != nil {
				_ = enc.Encode(uidPeerReply{Error: err.Error()})
			} else {
				_ = enc.Encode(uidPeerReply{Report: &report})
			}
			continue
		}
		if strings.HasPrefix(command, "probe ") {
			// No private token: a public discovery WebSocket must never accept the
			// ordinary control RPC shape. An initial challenge is expected; no RPC reply.
			target := strings.TrimPrefix(command, "probe ")
			c, stop := context.WithTimeout(ctx, time.Second)
			ws, _, e := websocket.Dial(c, target, &websocket.DialOptions{HTTPHeader: http.Header{}})
			if e == nil {
				var challenge json.RawMessage
				e = wsjson.Read(c, ws, &challenge)
				if e == nil {
					e = wsjson.Write(c, ws, map[string]any{"id": "unauthorized", "version": 1, "operation": "release", "params": map[string]any{"id": "anything"}})
				}
				var response json.RawMessage
				if e == nil {
					e = wsjson.Read(c, ws, &response)
					if e == nil {
						e = fmt.Errorf("public endpoint accepted control RPC: %s", response)
					} else {
						e = nil
					}
				}
				_ = ws.CloseNow()
			}
			stop()
			if e != nil {
				_ = enc.Encode(uidPeerReply{Error: e.Error()})
			} else {
				_ = enc.Encode(uidPeerReply{})
			}
			continue
		}
		if command != "stop" {
			cancel()
			<-done
			fail(fmt.Errorf("unknown fixture command"))
		}
		// Cancel before release so releasing capacity cannot activate the queued work.
		var q, h model.Lease
		if err = call("cancel", map[string]any{"id": queued.ID}, &q); err == nil {
			err = call("release", map[string]any{"id": held.ID}, &h)
		}
		cancel()
		serveErr := <-done
		if err != nil {
			fail(err)
		}
		if serveErr != nil {
			fail(serveErr)
		}
		if q.State != "terminal" || h.State != "terminal" {
			fail(fmt.Errorf("own work did not settle"))
		}
		_ = enc.Encode(uidPeerReply{Stopped: true})
		return
	}
	cancel()
	<-done
	if err = scanner.Err(); err != nil {
		fail(err)
	}
	fail(fmt.Errorf("parent ended before explicit settlement"))
}

type uidPeerChild struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	replies    chan uidPeerReply
	done       chan error
	ready      uidPeerReply
	joined     bool
	decodeDone chan struct{}
}

func TestLocalPresenceAcrossExistingLinuxUsers(t *testing.T) {
	if os.Getenv("ARM_TEST_PEERS_DIFFERENT_UID") != "1" {
		t.Skip("explicit Linux-root existing-user acceptance fixture; not run")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("opt-in fixture requires Linux root and two existing non-root users")
	}
	names := []string{os.Getenv("ARM_PEER_TEST_OWNER"), os.Getenv("ARM_PEER_TEST_OTHER")}
	if names[0] == "" {
		names[0] = "daemon"
	}
	if names[1] == "" {
		names[1] = "nobody"
	}
	root, err := os.MkdirTemp("/tmp", "arm-peer-users-test-")
	if err != nil {
		t.Fatal(err)
	}
	removeFixture := false
	defer func() {
		if removeFixture {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		} else {
			t.Logf("retained fixture with failed or unresolved original ownership: %s", root)
		}
	}()
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "peer.test")
	dst, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		src.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	_ = src.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy own test binary: %v %v", copyErr, closeErr)
	}
	var children []*uidPeerChild
	// Cleanup always joins the original Wait handle before removing fixture files.
	defer func() {
		settled := true
		for _, child := range children {
			if !child.joined {
				_ = child.cmd.Process.Kill()
				select {
				case <-child.done:
					child.joined = true
				case <-time.After(10 * time.Second):
					settled = false
					t.Errorf("original child did not join")
				}
			}
			select {
			case <-child.decodeDone:
			case <-time.After(time.Second):
				settled = false
				t.Error("original decoder did not join")
			}
			if child.ready.Record != "" {
				if _, err := os.Lstat(filepath.Join("/tmp", child.ready.Record)); !os.IsNotExist(err) {
					settled = false
					t.Errorf("original public record cleanup unresolved: %v", err)
				}
			}
		}
		removeFixture = settled && !t.Failed()
	}()
	uids := map[uint32]bool{}
	receive := func(child *uidPeerChild) uidPeerReply {
		t.Helper()
		select {
		case reply, ok := <-child.replies:
			if !ok {
				t.Fatal("child protocol ended")
			}
			if reply.Error != "" {
				t.Fatal(reply.Error)
			}
			return reply
		case <-time.After(12 * time.Second):
			t.Fatal("child protocol timeout")
		}
		return uidPeerReply{}
	}
	send := func(child *uidPeerChild, command string) uidPeerReply {
		t.Helper()
		if _, err := fmt.Fprintln(child.stdin, command); err != nil {
			t.Fatal(err)
		}
		return receive(child)
	}
	for i, name := range names {
		account, err := user.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		uid64, e1 := strconv.ParseUint(account.Uid, 10, 32)
		gid64, e2 := strconv.ParseUint(account.Gid, 10, 32)
		uid, gid := uint32(uid64), uint32(gid64)
		if e1 != nil || e2 != nil || uid == 0 || uids[uid] {
			t.Fatal("need distinct existing non-root UIDs")
		}
		uids[uid] = true
		home := filepath.Join(root, fmt.Sprintf("home-%d", i))
		if err = os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.Chown(home, int(uid), int(gid)); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "-test.run=^TestPeerDifferentUIDHelperProcess$")
		cmd.Env = append(os.Environ(), "ARM_PEER_UID_HELPER=1", "ARM_PEER_SERVE_HOME="+home, "ARM_HOME="+home, "ARM_PEER_EXPECT_UID="+account.Uid, "ARM_PEER_EXPECT_GID="+account.Gid, "GOMAXPROCS=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
		stdout, stdoutChild, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = stdoutChild
		cmd.Stderr = os.Stderr
		child := &uidPeerChild{cmd: cmd, stdin: stdin, replies: make(chan uidPeerReply, 2), done: make(chan error, 1), decodeDone: make(chan struct{})}
		if err = cmd.Start(); err != nil {
			stdout.Close()
			stdoutChild.Close()
			t.Fatal(err)
		}
		_ = stdoutChild.Close()
		children = append(children, child)
		go func() {
			defer close(child.decodeDone)
			defer stdout.Close()
			defer close(child.replies)
			decoder := json.NewDecoder(io.LimitReader(stdout, 4<<20))
			for {
				var reply uidPeerReply
				if decoder.Decode(&reply) != nil {
					return
				}
				child.replies <- reply
			}
		}()
		go func() { child.done <- cmd.Wait() }()
		child.ready = receive(child)
		if child.ready.UID != int(uid) || child.ready.GID != int(gid) || child.ready.Held.State != "active" || child.ready.Queued.State != "queued" {
			t.Fatalf("bad native fixture: %+v", child.ready)
		}
	}
	for i, child := range children {
		other := children[1-i].ready
		matched := false
		for until := time.Now().Add(8 * time.Second); time.Now().Before(until); time.Sleep(100 * time.Millisecond) {
			report := send(child, "sample").Report
			if report == nil {
				t.Fatal("missing summary")
			}
			for _, instance := range report.Instances {
				if instance.Host == other.Host && instance.UID == uint32(other.UID) && instance.Snapshot != nil {
					s := instance.Snapshot
					if s.Held.Memory == 700 && s.Queued.Memory == 600 && s.HeldCount == 1 && s.QueuedCount == 1 && len(s.Items) == 2 {
						if s.Items[0].Expires == nil || !s.Items[0].Expires.Equal(*other.Held.Expires) || s.Items[1].QueuePosition != 1 || s.Items[1].RequestedTTL != time.Minute {
							t.Fatal("expiry/queue timing mismatch")
						}
						matched = true
					}
				}
			}
			raw, _ := json.Marshal(report)
			for _, private := range []string{other.Held.ID, other.Queued.ID, "private child held purpose", "private child queued purpose", root} {
				if strings.Contains(string(raw), private) {
					t.Fatalf("private metadata leaked %q", private)
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			t.Fatal("different-UID proof/held/queue/expiry not observed")
		}
		_ = send(child, "probe "+other.URL)
	}
	// A control probe cannot release the other user's claims.
	for _, child := range children {
		report := send(child, "sample").Report
		if report == nil || report.Local.Held.Memory != 700 || report.Local.Queued.Memory != 600 {
			t.Fatal("public peer endpoint altered own claims")
		}
	}
	for _, child := range children {
		if !send(child, "stop").Stopped {
			t.Fatal("missing original server settlement")
		}
		select {
		case err := <-child.done:
			child.joined = true
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("original child failed to join")
		}
		_ = child.stdin.Close()
		if _, err = os.Lstat(filepath.Join("/tmp", child.ready.Record)); !os.IsNotExist(err) {
			t.Fatalf("known public record not removed: %v", err)
		}
	}
}
