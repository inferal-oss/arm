//go:build arm_devtrace

package meshops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yrashk/arm/internal/local"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
)

type reconnectWitness struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Output string `json:"output"`
	Facts  string `json:"facts"`
}
type reconnectObserved map[string]any
type reconnectNativeArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Raw    []byte `json:"raw_base64"`
}

type reconnectConfirmationInput struct {
	Attempt        string          `json:"attempt"`
	Plan           json.RawMessage `json:"plan"`
	SHA256         string          `json:"sha256"`
	ReviewRevision int64           `json:"review_revision"`
	Accepted       bool            `json:"accepted"`
}
type reconnectRawFact struct {
	ConfirmationInputs map[string]reconnectConfirmationInput `json:"confirmation_inputs"`
	Key                string                                `json:"key"`
	Artifacts          map[string]reconnectNativeArtifact    `json:"artifacts"`
	Action             string                                `json:"action"`
	Source             mesh.Config                           `json:"source"`
	Destination        mesh.Config                           `json:"destination"`
	OldCoord           mesh.Coordination                     `json:"old_coord"`
	OldLife            mesh.LifecycleOperation               `json:"old_lifecycle"`
	Root               json.RawMessage                       `json:"root,omitempty"`
	Active             json.RawMessage                       `json:"active,omitempty"`
	Resolved           *ReconnectRequest                     `json:"resolved,omitempty"`
	Inspection         *mesh.LifecycleInspection             `json:"last_remote_inspection,omitempty"`
	PairCalls          int                                   `json:"pair_calls"`
	Ensures            int                                   `json:"ensures"`
}

var errReconnectCut = errors.New("owned model prefix cut reached")

func reconnectFormalPath(path, base string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasPrefix(path, base+string(os.PathSeparator)) {
		return fmt.Errorf("owned formal path required")
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil || parent != filepath.Dir(path) {
		return fmt.Errorf("formal symlink/unknown parent refused")
	}
	if info, e := os.Lstat(path); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("formal regular file required")
	}
	return nil
}
func reconnectJSONUnique(raw []byte) error {
	return reconnectJSONUniqueBound(raw, 16384, 8192)
}

func reconnectJSONUniqueBound(raw []byte, scalarLimit, memberLimit int) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid formal UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	members := 0
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return fmt.Errorf("formal JSON depth")
		}
		token, e := decoder.Token()
		if e != nil {
			return e
		}
		delim, ok := token.(json.Delim)
		if !ok {
			if v, ok := token.(string); ok && (len(v) > scalarLimit || strings.ContainsRune(v, 0)) {
				return fmt.Errorf("formal scalar bound")
			}
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, e := decoder.Token()
				if e != nil {
					return e
				}
				name, ok := key.(string)
				if !ok || seen[name] || len(name) > 128 {
					return fmt.Errorf("duplicate/bounded formal member")
				}
				seen[name] = true
				members++
				if members > memberLimit {
					return fmt.Errorf("formal member bound")
				}
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := decoder.Token()
			if e != nil || end != json.Delim('}') {
				return fmt.Errorf("formal object framing")
			}
		case '[':
			for decoder.More() {
				members++
				if members > memberLimit {
					return fmt.Errorf("formal array bound")
				}
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := decoder.Token()
			if e != nil || end != json.Delim(']') {
				return fmt.Errorf("formal array framing")
			}
		default:
			return fmt.Errorf("formal JSON delimiter")
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := decoder.Token(); e != io.EOF {
		return fmt.Errorf("one formal JSON value required")
	}
	return nil
}
func reconnectValidateStates(rows []reconnectObserved) error {
	fields := map[string]int{}
	for _, k := range strings.Fields("schema action old_coord phase local_pair remote_pair local_state remote_state local_grant remote_grant active_review active_approval") {
		fields[k] = 0
	}
	for _, k := range strings.Fields("old_obligation resolved intent review approved child history_unknown identity_ok binding_ok active_accepted") {
		fields[k] = 1
	}
	for _, k := range strings.Fields("pair_calls ensures") {
		fields[k] = 2
	}
	labels := strings.Fields("init blocked handoff resolve dry review approve decline remote_pair local_pair source_activate target_activate complete unknown child_review lookup old_cleanup identity_refused revision_refused")
	if len(rows) < 2 || len(rows) > 128 {
		return fmt.Errorf("bounded complete trace required")
	}
	for _, row := range rows {
		if len(row) != len(fields) {
			return fmt.Errorf("closed formal state required")
		}
		for k, kind := range fields {
			v, ok := row[k]
			if !ok {
				return fmt.Errorf("missing field %s", k)
			}
			switch kind {
			case 0:
				s, ok := v.(string)
				if !ok || len(s) > 128 || s == "" {
					return fmt.Errorf("bounded formal string %s", k)
				}
				for _, r := range s {
					if r < 32 || r > 126 {
						return fmt.Errorf("ASCII formal scalar %s", k)
					}
				}
			case 1:
				if _, ok := v.(bool); !ok {
					return fmt.Errorf("formal boolean %s", k)
				}
			case 2:
				n, ok := v.(json.Number)
				if !ok {
					return fmt.Errorf("formal integer %s", k)
				}
				i, e := n.Int64()
				if e != nil || i < 0 || i > 4 {
					return fmt.Errorf("formal bounded integer %s", k)
				}
			}
		}
		if row["schema"] != "arm.mesh-reconnect/v1" {
			return fmt.Errorf("unsupported formal schema")
		}
		found := false
		for _, label := range labels {
			if row["action"] == label {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown formal action")
		}
	}
	return nil
}

func reconnectFormalRead(path, base string, v any) error {
	if e := reconnectFormalPath(path, base); e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	current, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || !os.SameFile(info, current) || info.Size() > 1<<20 {
		return fmt.Errorf("bounded regular formal input required")
	}
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return e
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("formal input exceeds 1MiB")
	}
	if e = reconnectJSONUnique(raw); e != nil {
		return e
	}
	return mesh.Decode(raw, v)
}
func reconnectFormalWrite(path, base string, v any) error {
	if e := reconnectFormalPath(path, base); e != nil {
		return e
	}
	raw, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if len(raw)+1 > 4<<20 {
		return fmt.Errorf("complete newline-inclusive formal artifact exceeds 4MiB")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write(append(raw, '\n'))
	if e == nil {
		e = f.Sync()
	}
	return e
}

type reconnectDriver struct {
	t                   *testing.T
	f                   *reconnectFixture
	key                 string
	old                 mesh.LifecycleOperation
	oldPair             string
	oldLocal, oldRemote mesh.Identity
	oldGrant            mesh.Grant
	resolved            *ReconnectRequest
	current             *Coordinator
	lastInspection      *mesh.LifecycleInspection
	expected            []reconnectObserved
	actual              []reconnectObserved
	facts               []reconnectRawFact
	stopped             bool
	recordOnly          bool
	control             string
	confirmationInputs  map[string]reconnectConfirmationInput
	lookupReceipts      []Operation
	mismatch            *reconnectMismatch
}

func newReconnectDriver(t *testing.T, expected []reconnectObserved, recording ...bool) *reconnectDriver {
	f := newReconnectFixture(t, true)
	d := &reconnectDriver{t: t, f: f, key: "formal-reconnect", oldPair: f.prior.Pair, oldLocal: f.source.Config().Identity, oldRemote: f.destination.Config().Identity, oldGrant: f.source.Config().Peers[0].Grant, expected: expected, recordOnly: len(recording) > 0 && recording[0], confirmationInputs: map[string]reconnectConfirmationInput{}}
	c := f.coordinator()
	call, ssh := c.Call, c.SSH
	c.Call = func(ctx context.Context, op string, q, out any) error {
		if op != "mesh_setup_revoke" {
			return call(ctx, op, q, out)
		}
		r := q.(DisconnectRequest)
		if e := f.source.RevokeExact(r.Key, r.Host, r.Pair, r.Fingerprint, r.Revision, r.Reason); e != nil {
			return e
		}
		return assign(out, map[string]bool{"revoked": true})
	}
	c.SSH = func(ctx context.Context, destination, binary string, q any, pin meshssh.Identity, out any) (meshssh.Identity, error) {
		if r, ok := q.(Helper); ok && r.Action == "lifecycle_revoke" {
			e := f.destination.RevokeExact(r.Revoke.Key, r.Revoke.Host, r.Revoke.Pair, r.Revoke.Fingerprint, r.Revoke.Revision, r.Revoke.Reason)
			if e != nil {
				return pin, e
			}
			return meshssh.Identity{Account: "fixture-user", HostKey: "SHA256:fixture"}, fmt.Errorf("actual revoke committed; fixture discards its acknowledgement")
		}
		return ssh(ctx, destination, binary, q, pin, out)
	}
	if code, e := c.Disconnect("formal-old-disconnect", f.draft.Host, "retained unknown original destination result", true, false); e != nil || code != 0 {
		t.Fatal(code, e)
	}
	old, ok, e := mesh.ReadLifecycleOperation(f.home, "formal-old-disconnect")
	if e != nil || !ok || old.Phase != "remote-pending" {
		t.Fatal("genuine coordinator lost-ACK initialization failed", old, e)
	}
	d.old = old
	if e = d.emit("init"); e != nil && !errors.Is(e, errReconnectCut) {
		t.Fatal(e)
	}
	return d
}
func (d *reconnectDriver) want() string {
	if len(d.actual) >= len(d.expected) {
		return ""
	}
	return d.expected[len(d.actual)]["action"].(string)
}
func reconnectArtifact(path string, v any) (reconnectNativeArtifact, error) {
	f, e := os.Open(path)
	if e != nil {
		return reconnectNativeArtifact{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return reconnectNativeArtifact{}, fmt.Errorf("bounded native snapshot required")
	}
	current, e := os.Lstat(path)
	if e != nil || !os.SameFile(info, current) {
		return reconnectNativeArtifact{}, fmt.Errorf("native snapshot identity changed")
	}
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return reconnectNativeArtifact{}, fmt.Errorf("bounded native snapshot read: %v", e)
	}
	if e = mesh.Decode(raw, v); e != nil {
		return reconnectNativeArtifact{}, e
	}
	sum := sha256.Sum256(raw)
	return reconnectNativeArtifact{Path: path, SHA256: hex.EncodeToString(sum[:]), Raw: raw}, nil
}
func reconnectCoordPath(home, key string) string {
	p, _ := mesh.LifecycleOperationPath(home, key)
	return filepath.Join(filepath.Dir(p), "coordinator-"+filepath.Base(p))
}

func (d *reconnectDriver) sample(action string) (reconnectObserved, reconnectRawFact, error) {
	f := d.f
	artifacts := map[string]reconnectNativeArtifact{}
	read := func(name, path string, out any) error {
		artifact, e := reconnectArtifact(path, out)
		if e == nil {
			artifacts[name] = artifact
		}
		return e
	}
	var source, destination mesh.Config
	if e := read("source_config", filepath.Join(f.home, "mesh", "config.json"), &source); e != nil {
		return nil, reconnectRawFact{}, e
	}
	if e := read("destination_config", filepath.Join(f.target, "mesh", "config.json"), &destination); e != nil {
		return nil, reconnectRawFact{}, e
	}
	if len(source.Peers) != 1 || len(destination.Peers) != 1 {
		return nil, reconnectRawFact{}, fmt.Errorf("one exact native peer required")
	}
	var oldCoord mesh.Coordination
	if e := read("old_coord", reconnectCoordPath(f.home, "formal-old-disconnect"), &oldCoord); e != nil {
		return nil, reconnectRawFact{}, e
	}
	oldPath, _ := mesh.LifecycleOperationPath(f.home, "formal-old-disconnect")
	var oldLife mesh.LifecycleOperation
	if e := read("old_lifecycle", oldPath, &oldLife); e != nil {
		return nil, reconnectRawFact{}, e
	}
	inputs := map[string]reconnectConfirmationInput{}
	for key, input := range d.confirmationInputs {
		input.Plan = append(json.RawMessage{}, input.Plan...)
		inputs[key] = input
	}
	fact := reconnectRawFact{ConfirmationInputs: inputs, Key: d.key, Artifacts: artifacts, Action: action, Source: source, Destination: destination, OldCoord: oldCoord, OldLife: oldLife, Resolved: d.resolved, Inspection: d.lastInspection, PairCalls: f.pairCalls, Ensures: f.ensures}
	var root mesh.Coordination
	err := read("root", reconnectCoordPath(f.home, d.key), &root)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, fact, err
	}
	phase := "absent"
	intent, review, approved, child, unknown := false, false, false, false, false
	bindingOK := true
	if d.resolved != nil {
		bindingOK = d.resolved.Binding.Prior.Pair == d.oldPair && sameReconnectPrincipal(d.resolved.Binding.Local, d.oldLocal) && sameReconnectPrincipal(d.resolved.Binding.Prior.Identity, d.oldRemote)
	}
	var activeRecord *mesh.Coordination
	if exists {
		fact.Root = append(json.RawMessage{}, artifacts["root"].Raw...)
		fact.Active = append(json.RawMessage{}, artifacts["root"].Raw...)
		activeRecord = &root
		intent = true
		review = len(root.Review) > 0
		rootReview, _, _ := reconnectActiveConsent(root, artifacts["root"], inputs)
		review = rootReview == "exact"
		approved = root.Approved
		unknown = root.Phase == "incomplete"
		phase = root.Phase
		var saved StartRequest
		digest, _ := mesh.ReviewDigest(*d.resolved)
		bindingOK = bindingOK && mesh.Decode(root.Request, &saved) == nil && saved.Kind == "reconnect" && saved.Reconnect != nil && root.Digest == digest
		if bindingOK {
			a, _ := mesh.ReviewDigest(saved)
			b, _ := mesh.ReviewDigest(StartRequest{Key: d.key, Kind: "reconnect", Reconnect: d.resolved})
			bindingOK = a == b
		}
		if d.current != nil && d.current.guard != nil && d.current.guard.Key() != d.key {
			var active mesh.Coordination
			if e := read("active", reconnectCoordPath(f.home, d.current.guard.Key()), &active); e != nil {
				return nil, fact, e
			}
			fact.Active = append(json.RawMessage{}, artifacts["active"].Raw...)
			activeRecord = &active
			phase = active.Phase
			child = active.ParentKey == d.key
			bindingOK = bindingOK && child && active.Digest == root.Digest
			var childRequest StartRequest
			if mesh.Decode(active.Request, &childRequest) != nil {
				bindingOK = false
			} else {
				a, _ := mesh.ReviewDigest(childRequest)
				b, _ := mesh.ReviewDigest(StartRequest{Key: d.key, Kind: "reconnect", Reconnect: d.resolved})
				bindingOK = bindingOK && a == b
			}
		}
	}
	oldState := "pending"
	if oldCoord.Phase == "handed_off" {
		oldState = "handed_off"
	}
	var oldResult struct {
		Pending bool `json:"remote_pending"`
	}
	oldObligation := oldLife.Phase == "remote-pending" && json.Unmarshal(oldCoord.Result, &oldResult) == nil && oldResult.Pending
	fresh := f.draft
	if d.resolved != nil {
		fresh = d.resolved.Draft
	}
	pair := connectPair(d.key, d.oldLocal.Host, d.oldRemote.Host)
	kind := func(value string) string {
		if value == d.oldPair {
			return "old"
		}
		if value == pair {
			return "new"
		}
		return "other"
	}
	grant := func(actual, want mesh.Grant) string {
		if reflect.DeepEqual(actual, d.oldGrant) {
			return "old"
		}
		if reflect.DeepEqual(actual, want) {
			return "fresh"
		}
		return "other"
	}
	identityOK := sameReconnectPrincipal(destination.Identity, d.oldRemote)
	if d.lastInspection != nil {
		identityOK = d.lastInspection.Identity != nil && sameReconnectPrincipal(*d.lastInspection.Identity, d.oldRemote)
	}
	activeReview, activeApproval, activeAccepted := "absent", "absent", false
	if activeRecord != nil {
		artifact := artifacts["root"]
		if item, ok := artifacts["active"]; ok {
			artifact = item
		}
		activeReview, activeApproval, activeAccepted = reconnectActiveConsent(*activeRecord, artifact, inputs)
	}
	v := reconnectObserved{"active_review": activeReview, "active_approval": activeApproval, "active_accepted": activeAccepted, "schema": "arm.mesh-reconnect/v1", "action": action, "old_coord": oldState, "old_obligation": oldObligation, "resolved": d.resolved != nil, "intent": intent, "review": review, "approved": approved, "phase": phase, "child": child, "history_unknown": unknown, "identity_ok": identityOK, "binding_ok": bindingOK, "local_pair": kind(source.Peers[0].Pair), "remote_pair": kind(destination.Peers[0].Pair), "local_state": source.Peers[0].State, "remote_state": destination.Peers[0].State, "local_grant": grant(source.Peers[0].Grant, fresh.Reverse), "remote_grant": grant(destination.Peers[0].Grant, fresh.Forward), "pair_calls": f.pairCalls, "ensures": f.ensures}
	return v, fact, nil
}
func (d *reconnectDriver) emit(action string) error {
	v, fact, e := d.sample(action)
	if e != nil {
		return e
	}
	if d.recordOnly {
		if len(d.actual) >= 128 {
			return fmt.Errorf("bounded actual control rows")
		}
		d.actual = append(d.actual, v)
		d.facts = append(d.facts, fact)
		return nil
	}
	if len(d.actual) >= len(d.expected) {
		return errReconnectCut
	}
	a, _ := json.Marshal(v)
	b, _ := json.Marshal(d.expected[len(d.actual)])
	if !bytes.Equal(a, b) {
		d.mismatch = &reconnectMismatch{Actual: v, Expected: d.expected[len(d.actual)], Fact: fact, Row: len(d.actual) + 1}
		return fmt.Errorf("actual %s differs from TLC at row %d\nactual %s\nexpected %s", action, len(d.actual)+1, a, b)
	}
	d.actual = append(d.actual, v)
	d.facts = append(d.facts, fact)
	if len(d.actual) == len(d.expected) {
		d.stopped = true
		return errReconnectCut
	}
	return nil
}
func (d *reconnectDriver) connect(recovery bool) error {
	c := d.f.coordinator()
	d.current = c
	call, ssh := c.Call, c.SSH
	c.Confirm = func(plan any, _, _ bool) error {
		op, e := c.guard.Read()
		if e != nil {
			return e
		}
		raw, e := json.Marshal(plan)
		if len(raw) > 256<<10 {
			return fmt.Errorf("bounded actual confirmation plan required")
		}
		if e != nil {
			return e
		}
		hash, e := mesh.ReviewDigest(plan)
		if e != nil {
			return e
		}
		if _, exists := d.confirmationInputs[op.Key]; exists {
			return fmt.Errorf("confirmation input already captured for exact attempt")
		}
		d.confirmationInputs[op.Key] = reconnectConfirmationInput{Attempt: op.Key, Plan: append(json.RawMessage{}, raw...), SHA256: hash, ReviewRevision: op.ReviewRevision}
		label := "review"
		if recovery {
			label = "child_review"
		}
		if e := d.checkpoint(label); e != nil {
			return e
		}
		if d.want() == "decline" || (d.recordOnly && d.control == "decline") {
			return fmt.Errorf("explicit exact review declined")
		}
		if d.want() == "revision_refused" || (d.recordOnly && d.control == "revision") {
			prior, e := d.f.source.Resolve(d.oldRemote.Host)
			if e != nil {
				return e
			}
			_, e = d.f.source.PairReviewed(prior.Identity, prior.Grant, strings.Repeat("f", 64), "concurrent separate exact operator enrollment", true, prior.Revision)
			if e != nil {
				return e
			}
		}
		input := d.confirmationInputs[op.Key]
		input.Accepted = true
		d.confirmationInputs[op.Key] = input
		return nil
	}
	c.Call = func(ctx context.Context, op string, q, out any) error {
		if op == "mesh_setup_apply" {
			if e := d.checkpoint("approve"); e != nil {
				return e
			}
		}
		e := call(ctx, op, q, out)
		if e != nil {
			return e
		}
		switch op {
		case "mesh_pair":
			return d.checkpoint("local_pair")
		case "mesh_activate":
			return d.checkpoint("source_activate")
		}
		return nil
	}
	c.SSH = func(ctx context.Context, destination, binary string, q any, pin meshssh.Identity, out any) (meshssh.Identity, error) {
		id, e := ssh(ctx, destination, binary, q, pin, out)
		if e != nil {
			return id, e
		}
		switch r := q.(type) {
		case Helper:
			if r.Action == "lifecycle_inspect" {
				v := out.(*mesh.LifecycleInspection)
				copy := *v
				d.lastInspection = &copy
			}
		case Bootstrap:
			switch r.Action {
			case "pair":
				if e = d.checkpoint("remote_pair"); e != nil {
					return id, e
				}
				if d.want() == "unknown" || (d.recordOnly && d.control == "lostpair") {
					return id, fmt.Errorf("actual target pair committed; acknowledgement discarded")
				}
			case "activate":
				return id, d.checkpoint("target_activate")
			}
		}
		return id, nil
	}
	var code int
	var e error
	if recovery {
		code, e = c.ResumeReconnect(d.key, *d.resolved, true, false)
	} else {
		code, e = c.Reconnect(d.key, *d.resolved, true, false)
	}
	if d.stopped {
		return nil
	}
	if d.recordOnly {
		saved, ok, readErr := mesh.ReadCoordination(d.f.home, c.guard.Key())
		if readErr != nil || !ok {
			return fmt.Errorf("actual control receipt: %v", readErr)
		}
		if code == 0 && e == nil && saved.Phase == "completed" {
			return d.emit("complete")
		}
		if code != 0 && e != nil && saved.Worker == "" {
			if d.control == "lostpair" && saved.Phase == "incomplete" {
				return d.emit("unknown")
			}
			if d.control == "decline" && saved.Phase == "rejected" {
				return d.emit("decline")
			}
			if d.control == "revision" && saved.Phase == "rejected" {
				return d.emit("revision_refused")
			}
		}
		return fmt.Errorf("actual control failed to settle: code=%d err=%v phase=%s worker=%s", code, e, saved.Phase, saved.Worker)
	}
	switch d.want() {
	case "unknown", "decline", "revision_refused":
		return d.emit(d.want())
	case "complete":
		if code != 0 || e != nil {
			return fmt.Errorf("expected actual completion: %d %v", code, e)
		}
		return d.emit("complete")
	}
	return fmt.Errorf("unexpected actual coordinator result %d %v; next=%s", code, e, d.want())
}
func (d *reconnectDriver) step() error {
	switch next := d.want(); next {
	case "blocked":
		_, e := ResolveReconnect(d.f.home, d.key, d.f.draft)
		if e == nil {
			return fmt.Errorf("pending old exclusion was bypassed")
		}
		return d.emit(next)
	case "handoff":
		op, _, e := mesh.ReadCoordination(d.f.home, "formal-old-disconnect")
		if e != nil {
			return e
		}
		hash, e := mesh.CoordinationRecoveryHash(op)
		if e != nil {
			return e
		}
		if _, e = mesh.HandoffCoordination(d.f.home, op.Key, op.Revision, hash, "explicit retained-obligation model fixture handoff"); e != nil {
			return e
		}
		return d.emit(next)
	case "resolve":
		r, e := ResolveReconnect(d.f.home, d.key, d.f.draft)
		if e != nil {
			return e
		}
		d.resolved = &r
		return d.emit(next)
	case "dry":
		c := d.f.coordinator()
		if code, e := c.Reconnect(d.key, *d.resolved, false, true); e != nil || code != 0 {
			return fmt.Errorf("actual dry %d %v", code, e)
		}
		return d.emit(next)
	case "review":
		return d.connect(false)
	case "child_review":
		return d.connect(true)
	case "lookup":
		return d.offlineLookup()
	case "old_cleanup":
		p := d.old
		err := d.f.destination.RevokeExact("another-delayed-key", d.oldLocal.Host, p.Pair, d.oldLocal.Fingerprint, p.RemoteRevision, p.Reason)
		if err == nil {
			return fmt.Errorf("old delayed tuple unexpectedly matched replacement")
		}
		return d.emit(next)
	case "identity_refused":
		d.f.inspectRemote = func(v *mesh.LifecycleInspection) { other := d.oldLocal; v.Identity = &other; v.Host = other.Host }
		c := d.f.coordinator()
		ssh := c.SSH
		c.SSH = func(ctx context.Context, a, b string, q any, pin meshssh.Identity, out any) (meshssh.Identity, error) {
			id, e := ssh(ctx, a, b, q, pin, out)
			if h, ok := q.(Helper); e == nil && ok && h.Action == "lifecycle_inspect" {
				copy := *out.(*mesh.LifecycleInspection)
				d.lastInspection = &copy
			}
			return id, e
		}
		if code, e := c.Reconnect(d.key, *d.resolved, true, false); e == nil || code == 0 {
			return fmt.Errorf("wrong reported node inherited enrollment")
		}
		return d.emit(next)
	default:
		return fmt.Errorf("unsupported actual witness action %q", next)
	}
}
func TestReconnectFormalTLCWitnesses(t *testing.T) {
	manifest := os.Getenv("ARM_RECONNECT_FORMAL_MANIFEST")
	if manifest == "" {
		t.Skip("dedicated pinned TLC witness manifest required")
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(filepath.Join(cwd, "../.."))
	base := filepath.Join(repo, ".tla")
	var rows []reconnectWitness
	if e = reconnectFormalRead(manifest, base, &rows); e != nil {
		t.Fatal(e)
	}
	if len(rows) == 0 || len(rows) > 24 {
		t.Fatal("bounded nonempty witness roster required")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Target] || row.Target == "" {
			t.Fatal("duplicate target")
		}
		seen[row.Target] = true
		t.Run(row.Target, func(t *testing.T) {
			var expected []reconnectObserved
			if e := reconnectFormalRead(row.Path, base, &expected); e != nil {
				t.Fatal(e)
			}
			if e := reconnectValidateStates(expected); e != nil {
				t.Fatal(e)
			}
			d := newReconnectDriver(t, expected)
			t.Cleanup(func() {
				if t.Failed() {
					if err := d.retainFailure(row.Output+".failure", base); err != nil {
						t.Errorf("retain actual failed prefix: %v", err)
					}
				}
			})
			for !d.stopped {
				if e := d.step(); e != nil && !errors.Is(e, errReconnectCut) {
					t.Fatal(e)
				}
			}
			if e := reconnectFormalWrite(row.Output, base, d.actual); e != nil {
				t.Fatal(e)
			}
			if e := reconnectFormalWrite(row.Facts, base, d.facts); e != nil {
				t.Fatal(e)
			}
		})
	}
}

// This test compiles the dev-only exporter without launching SSH or any solver.
func TestReconnectFormalOutputBoundIncludesNewline(t *testing.T) {
	root := t.TempDir()
	var canonicalErr error
	root, canonicalErr = filepath.EvalSymlinks(root)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	path := filepath.Join(root, "artifact.json")
	// Quoted string length plus trailing newline is the complete artifact bound.
	if e := reconnectFormalWrite(path, root, strings.Repeat("x", (4<<20)-3)); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Size() != 4<<20 {
		t.Fatal(info, e)
	}
	if e := reconnectFormalWrite(filepath.Join(root, "too-large.json"), root, strings.Repeat("x", (4<<20)-2)); e == nil {
		t.Fatal("newline exceeded reader cap")
	}
}

func TestReconnectFormalObservationNegatives(t *testing.T) {
	manifest := os.Getenv("ARM_RECONNECT_FORMAL_MANIFEST")
	output := os.Getenv("ARM_RECONNECT_FORMAL_NEGATIVES")
	if manifest == "" || output == "" {
		t.Skip("dedicated actual model witness inventory required")
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	base := filepath.Join(filepath.Clean(filepath.Join(cwd, "../..")), ".tla")
	var rows []reconnectWitness
	if e = reconnectFormalRead(manifest, base, &rows); e != nil {
		t.Fatal(e)
	}
	var complete, recovered []reconnectObserved
	for _, row := range rows {
		if row.Target == "recovered" {
			if e = reconnectFormalRead(row.Path, base, &recovered); e != nil {
				t.Fatal(e)
			}
		}
		if row.Target == "complete" {
			if e = reconnectFormalRead(row.Path, base, &complete); e != nil {
				t.Fatal(e)
			}
		}
	}
	if complete == nil || recovered == nil {
		t.Fatal("complete and recovered positive witnesses required before actual corruption negatives")
	}
	negatives := map[string][]reconnectObserved{}
	negativeFacts := map[string][]reconnectRawFact{}
	for _, name := range []string{"actual-old-grant-on-new-pair", "actual-changed-original-binding", "actual-erased-old-obligation", "actual-child-missing-review", "actual-child-substituted-review", "actual-child-changed-review-revision", "actual-child-missing-approval", "actual-child-cleared-approval"} {
		t.Run(name, func(t *testing.T) {
			witness := complete
			if strings.HasPrefix(name, "actual-child-") {
				witness = recovered
			}
			d := newReconnectDriver(t, witness)
			t.Cleanup(func() {
				if t.Failed() {
					if err := d.retainFailure(filepath.Join(filepath.Dir(output), name+".failure"), base); err != nil {
						t.Errorf("retain actual negative setup prefix: %v", err)
					}
				}
			})
			for !d.stopped {
				if e := d.step(); e != nil && !errors.Is(e, errReconnectCut) {
					t.Fatal(e)
				}
			}
			if strings.HasPrefix(name, "actual-child-") {
				reconnectMutateChildConsent(t, d, strings.TrimPrefix(name, "actual-child-"))
			}
			switch name {
			case "actual-old-grant-on-new-pair":
				config := d.f.source.Config()
				config.Peers[0].Grant = d.oldGrant
				d.f.source.Close()
				if e := local.AtomicJSON(filepath.Join(d.f.home, "mesh", "config.json"), config); e != nil {
					t.Fatal(e)
				}
				store, e := mesh.Open(d.f.home)
				if e != nil {
					t.Fatal(e)
				}
				d.f.source = store
			case "actual-changed-original-binding":
				op, _, e := mesh.ReadCoordination(d.f.home, d.key)
				if e != nil {
					t.Fatal(e)
				}
				var request StartRequest
				if e = mesh.Decode(op.Request, &request); e != nil {
					t.Fatal(e)
				}
				request.Reconnect.Binding.Prior.Pair = strings.Repeat("e", 64)
				op.Request, _ = json.Marshal(request)
				path, e := mesh.LifecycleOperationPath(d.f.home, d.key)
				if e != nil {
					t.Fatal(e)
				}
				path = filepath.Join(filepath.Dir(path), "coordinator-"+filepath.Base(path))
				if e = local.AtomicJSON(path, op); e != nil {
					t.Fatal(e)
				}
			case "actual-erased-old-obligation":
				old, _, e := mesh.ReadLifecycleOperation(d.f.home, "formal-old-disconnect")
				if e != nil {
					t.Fatal(e)
				}
				old.Phase = "disconnected"
				path, e := mesh.LifecycleOperationPath(d.f.home, old.Key)
				if e != nil {
					t.Fatal(e)
				}
				if e = local.AtomicJSON(path, old); e != nil {
					t.Fatal(e)
				}
			}
			// Perform the real read-only key path against the actual mutated artifacts.
			// Refusal is allowed; a sampled corruption must not become accepted authority.
			request, e := ResolveReconnect(d.f.home, d.key, d.resolved.Draft)
			if e == nil {
				_, _ = d.f.coordinator().Reconnect(d.key, request, false, false)
			}
			observed, fact, e := d.sample("lookup")
			if e != nil {
				t.Fatal(e)
			}
			trace := append(append([]reconnectObserved{}, d.actual...), observed)
			a, _ := json.Marshal(trace[len(trace)-2])
			b, _ := json.Marshal(observed)
			if bytes.Equal(a, b) {
				t.Fatal("negative did not change actual state")
			}
			negatives[name] = trace
			negativeFacts[name] = append(append([]reconnectRawFact{}, d.facts...), fact)
		})
	}
	if e = reconnectWriteCases(output, base, negatives, negativeFacts); e != nil {
		t.Fatal(e)
	}
}

func TestReconnectFormalInputGuards(t *testing.T) {
	// Grammar-only fixture, not a claimed observed runtime/TLC history.
	row := `{"active_review":"absent","active_approval":"absent","active_accepted":false,"schema":"arm.mesh-reconnect/v1","action":"init","old_coord":"pending","old_obligation":true,"resolved":false,"intent":false,"review":false,"approved":false,"phase":"absent","child":false,"history_unknown":false,"identity_ok":true,"binding_ok":true,"local_pair":"old","remote_pair":"old","local_state":"revoked","remote_state":"revoked","local_grant":"old","remote_grant":"old","pair_calls":0,"ensures":0}`
	raw := []byte("[" + row + "," + row + "]")
	var rows []reconnectObserved
	if e := mesh.Decode(raw, &rows); e != nil {
		t.Fatal(e)
	}
	if e := reconnectValidateStates(rows); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"action":"init"`, `"action":null`, 1), strings.Replace(string(raw), `"pair_calls":0`, `"pair_calls":1.5`, 1), strings.Replace(string(raw), `"resolved":false,`, "", 1), strings.Replace(string(raw), `"schema":"arm.mesh-reconnect/v1"`, `"injected":true,"schema":"arm.mesh-reconnect/v1"`, 1)} {
		var value []reconnectObserved
		if e := mesh.Decode([]byte(bad), &value); e == nil {
			if e = reconnectValidateStates(value); e == nil {
				t.Fatal("invalid formal state accepted")
			}
		}
	}
	for _, bad := range []string{`{"a":1,"a":1}`, `{"a":1,"\u0061":1}`, `{} {}`, string([]byte{0xff}), strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)} {
		if e := reconnectJSONUnique([]byte(bad)); e == nil {
			t.Fatal("invalid closed JSON accepted")
		}
	}
}
