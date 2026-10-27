package workspaceroute

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

func directoryGraphFixture(t *testing.T, count int) (Open, Request, routing.GraphBundle) {
	t.Helper()
	now := time.Now().UTC()
	ids := make([]workspace.Identity, count)
	for i := range ids {
		home := t.TempDir()
		if e := os.Chmod(home, 0700); e != nil {
			t.Fatal(e)
		}
		store, e := mesh.InitPrepared(home, fmt.Sprintf("%032x", i+1), fmt.Sprintf("node-%d", i+1), "127.0.0.1:32001", "127.0.0.1:32001")
		if e != nil {
			t.Fatal(e)
		}
		v := store.Config().Identity
		ids[i] = workspace.Identity{Host: v.Host, Name: v.Name, Certificate: v.Certificate, Fingerprint: v.Fingerprint, SPKI: v.SPKI, Expires: v.Expires}
		store.Close()
	}
	m := workspace.Snapshot{Schema: 1, Workspace: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32), Directory: ids[0], Requester: ids[1].Host, RequesterGeneration: 1, Nonce: strings.Repeat("c", 64), Revision: 7, PolicyRevision: 1, Policy: workspace.DefaultPolicy(), Issued: now, ValidUntil: now.Add(time.Minute), TTL: time.Minute, Members: []workspace.MemberCard{}}
	for _, id := range ids {
		m.Members = append(m.Members, workspace.MemberCard{Identity: id, MembershipGeneration: 1, State: "active", Introducer: true, HostInbox: true, Revision: 1})
	}
	raw, _ := json.Marshal(m)
	h := sha256.Sum256(raw)
	m.Hash = hex.EncodeToString(h[:])
	g, e := workspacegraph.Build(routing.GraphScope(m, 1), nil, strings.Repeat("d", 32), 0)
	if e != nil {
		t.Fatal(e)
	}
	q := Request{Version: Version, Route: strings.Repeat("e", 32), Workspace: m.Workspace, Generation: m.Generation, Source: m.Requester, Target: m.Directory.Host, Operation: OperationRefresh, Refresh: &workspace.RefreshRequest{Source: workspace.Principal{Workspace: m.Workspace, Generation: m.Generation, Host: m.Requester, MembershipGeneration: 1, Fingerprint: ids[1].Fingerprint}, Nonce: m.Nonce}, Report: &workspacegraph.Report{Schema: 1, Workspace: m.Workspace, Generation: m.Generation, Source: m.Requester, SourceGeneration: 1, SourcePin: ids[1].Fingerprint, Sequence: 1, Nonce: m.Nonce, Issued: now, ValidUntil: now.Add(time.Minute), Neighbors: []workspacegraph.Neighbor{}}}
	o := Open{Version: Version, Workspace: m.Workspace, Generation: m.Generation, Route: q.Route, Source: q.Source, Target: q.Target, SourceGeneration: 1, TargetGeneration: 1, DirectoryRevision: 7, Path: []string{q.Source, q.Target}, Hop: 1, Class: ClassDirectory, Expires: now.Add(30 * time.Second), Pair: strings.Repeat("f", 64), Revision: 1}
	if e = q.Validate(o); e != nil {
		t.Fatal(e)
	}
	return o, q, routing.GraphBundle{Membership: m, Snapshot: g}
}
func graphRepliesBytes(t *testing.T, q Request, replies []Reply) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, r := range replies {
		if e := WriteReply(&b, q, r); e != nil {
			t.Fatal(e)
		}
	}
	return b.Bytes()
}
func TestDirectoryGraphCompleteFramingAndNoPartialAssembly(t *testing.T) {
	_, q, b := directoryGraphFixture(t, 4)
	replies, e := DirectoryReplies(q, b)
	if e != nil {
		t.Fatal(e)
	}
	raw := graphRepliesBytes(t, q, replies)
	got, e := ReadDirectoryBundle(bytes.NewReader(raw), q)
	if e != nil || !reflect.DeepEqual(got, b) {
		t.Fatal("complete graph differs", e)
	}
	request, _ := json.Marshal(q)
	if len(raw)+len(request)+4+GraphTransportReserve > MaxStreamBytes {
		t.Fatal("full framed transport budget was not reserved")
	}
	for name, bad := range map[string][]Reply{"missing-graph": replies[:len(replies)-1], "reversed": {replies[len(replies)-1], replies[0]}, "repeated-members": {replies[0], replies[0], replies[len(replies)-1]}} {
		t.Run(name, func(t *testing.T) {
			if got, e := ReadDirectoryBundle(bytes.NewReader(graphRepliesBytes(t, q, bad)), q); e == nil || got.Membership.Hash != "" || got.Snapshot.Hash != "" {
				t.Fatal("partial/mixed graph activated", e)
			}
		})
	}
	// A semantically valid page cannot replace the original nonce/source scope.
	changed := q
	copy := *q.Refresh
	copy.Nonce = strings.Repeat("9", 64)
	changed.Refresh = &copy
	if _, e := ReadDirectoryBundle(bytes.NewReader(raw), changed); e == nil {
		t.Fatal("another request nonce assembled")
	}
}
func TestGraphPayloadCannotEnterMessageOrLookup(t *testing.T) {
	_, q, _ := directoryGraphFixture(t, 2)
	for _, class := range []string{ClassMessage, ClassLookup} {
		o, m := messageFixture()
		o.Class = class
		if class == ClassLookup {
			m.Operation = OperationLookup
		}
		m.Report = q.Report
		if m.Validate(o) == nil {
			t.Fatal("foreign graph payload admitted", class)
		}
	}
}
func TestDirectoryGraphWholeResponsePreflightRefusesDenseBundle(t *testing.T) {
	_, q, b := directoryGraphFixture(t, 128)
	// Public display names may contain HTML-sensitive ASCII. encoding/json must
	// account for its actual escaping, not a guessed bytes-per-member allowance.
	for i := range b.Membership.Members {
		b.Membership.Members[i].Identity.Name = strings.Repeat("<", 128)
		b.Membership.Members[i].Identity.Certificate += strings.Repeat(" ", 4096)
	}
	b.Membership.Directory.Name = strings.Repeat("<", 128)
	b.Membership.Directory.Certificate += strings.Repeat(" ", 4096)
	b.Membership.Hash = ""
	raw, _ := json.Marshal(b.Membership)
	sum := sha256.Sum256(raw)
	b.Membership.Hash = hex.EncodeToString(sum[:])
	b.Snapshot.MembershipHash = b.Membership.Hash
	// Actual complete128-member roster plus a degree64 circulant graph. This is
	// within semantic node/neighbor/edge limits but may exceed bounded wire bytes;
	// no partial membership/graph or silent edge dropping is permissible.
	for i := 0; i < 128; i++ {
		for j := i + 1; j < 128; j++ {
			distance := min(j-i, 128-(j-i))
			if distance <= 32 {
				b.Snapshot.Edges = append(b.Snapshot.Edges, workspacegraph.Edge{A: b.Snapshot.Nodes[i].Host, B: b.Snapshot.Nodes[j].Host, Pair: fmt.Sprintf("%064x", i*128+j+1)})
			}
		}
	}
	b.Snapshot.Hash = b.Snapshot.Digest()
	if e := b.Snapshot.ValidateStructure(); e != nil {
		t.Fatal("dense fixture semantic bound changed", e)
	}
	replies, e := DirectoryReplies(q, b)
	if e == nil || replies != nil {
		t.Fatal("oversized complete bundle was partially accepted", e, len(replies))
	}
}

func TestDirectoryGraphRawFramedWhitespaceConsumesWholeBudget(t *testing.T) {
	_, q, b := directoryGraphFixture(t, 128)
	for i := 0; i < 128; i++ {
		for j := i + 1; j < 128; j++ {
			if min(j-i, 128-(j-i)) <= 32 {
				b.Snapshot.Edges = append(b.Snapshot.Edges, workspacegraph.Edge{A: b.Snapshot.Nodes[i].Host, B: b.Snapshot.Nodes[j].Host, Pair: fmt.Sprintf("%064x", i*128+j+1)})
			}
		}
	}
	b.Snapshot.Hash = b.Snapshot.Digest()
	replies, e := DirectoryReplies(q, b)
	if e != nil {
		t.Fatal("ordinary complete dense bundle no longer fits", e)
	}
	var padded bytes.Buffer
	for _, reply := range replies {
		raw, e := json.Marshal(reply)
		if e != nil {
			t.Fatal(e)
		}
		if len(raw) > MaxRequestBytes {
			t.Fatal("fixture page exceeds bound")
		}
		raw = append(raw, bytes.Repeat([]byte(" "), MaxRequestBytes-len(raw))...)
		padded.Write(framed(raw))
	}
	if padded.Len() < MaxStreamBytes {
		t.Fatal("fixture did not consume complete stream limit", padded.Len())
	}
	got, e := ReadDirectoryBundle(bytes.NewReader(padded.Bytes()), q)
	if e == nil || got.Membership.Hash != "" || got.Snapshot.Hash != "" {
		t.Fatal("raw whitespace bypassed aggregate bound", e)
	}
}
