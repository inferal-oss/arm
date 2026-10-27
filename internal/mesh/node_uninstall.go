package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
	"golang.org/x/sys/unix"
)

const UninstallSchema = "arm.node-uninstall/v1"

type UninstallPlan struct {
	Schema    string            `json:"schema"`
	Key       string            `json:"key"`
	Reason    string            `json:"reason"`
	Transfer  meshssh.Transfer  `json:"installation"`
	Ownership InstallOwnership  `json:"ownership"`
	Stop      nodestop.Prepared `json:"stop"`
	Recovery  string            `json:"retained_recovery_helper"`
	Preserved []string          `json:"preserved"`
}
type UninstallReceipt struct {
	Schema           string             `json:"schema"`
	Plan             UninstallPlan      `json:"plan"`
	SHA256           string             `json:"review_sha256"`
	Phase            string             `json:"phase"`
	RecoveryIdentity model.FileIdentity `json:"recovery_identity"`
	Updated          time.Time          `json:"updated_at"`
	Detail           string             `json:"detail,omitempty"`
}
type UninstallResult struct {
	Receipt        UninstallReceipt      `json:"receipt"`
	Stop           *nodestop.Observation `json:"-"`
	StopObserved   bool                  `json:"original_stop_observed"`
	ArtifactAbsent bool                  `json:"artifact_absent"`
	Detail         string                `json:"detail"`
}

func uninstallName(key string) string {
	s := sha256.Sum256([]byte(key))
	return hex.EncodeToString(s[:])
}
func UninstallStopKey(key string) string { return "uninstall-" + uninstallName(key) }
func UninstallRecoveryPath(q meshssh.Transfer, key string) string {
	return filepath.Join(q.Root, "recovery-"+uninstallName(key), "arm")
}
func uninstallReceiptPath(q meshssh.Transfer, key string) string {
	return filepath.Join(q.Root, "uninstall-"+uninstallName(key)+".json")
}
func (p UninstallPlan) Validate() error {
	if p.Schema != UninstallSchema || !ValidKey(p.Key) || p.Transfer.Validate() != nil || p.Transfer.ExpectedUID != p.Stop.Plan.Target.Owner || p.Ownership.Transfer != p.Transfer || p.Ownership.Schema != 1 || !p.Ownership.Root.BirthKnown || !p.Ownership.Artifact.BirthKnown || !p.Ownership.Binary.BirthKnown || p.Ownership.Removing != "" || len(p.Ownership.Homes) != 1 || p.Ownership.Homes[0] != p.Stop.Plan.Target.Home || p.Recovery != UninstallRecoveryPath(p.Transfer, p.Key) || p.Stop.Plan.Key != UninstallStopKey(p.Key) || p.Stop.Plan.Reason != p.Reason {
		return fmt.Errorf("invalid exact managed uninstall scope")
	}
	h, e := p.Stop.Plan.Digest()
	if e != nil || h != p.Stop.SHA256 {
		return fmt.Errorf("uninstall Stop plan digest differs")
	}
	if !reflect.DeepEqual(p.Preserved, uninstallPreserved()) {
		return fmt.Errorf("preservation disclosure required")
	}
	raw, e := json.Marshal(p)
	if e != nil || len(raw) > 48<<10 {
		return fmt.Errorf("complete uninstall review exceeds48KiB; nothing admitted")
	}
	return nil
}
func (p UninstallPlan) Digest() (string, error) {
	if e := p.Validate(); e != nil {
		return "", e
	}
	return ReviewDigest(p)
}

// PrepareUninstall proves original publication ownership without creating files.
func PrepareUninstall(q meshssh.Transfer, key, reason string, stop nodestop.Prepared) (UninstallPlan, error) {
	var p UninstallPlan
	root, unlock, e := provisionExistingLock(q)
	if e != nil {
		return p, e
	}
	defer unlock()
	r, e := readProvision(q)
	if e != nil {
		return p, e
	}
	if r.StartedHost != stop.Plan.Target.Host || r.StartHome != stop.Plan.Target.Home || q.ExpectedUID != stop.Plan.Target.Owner {
		return p, fmt.Errorf("installation receipt differs from exact running authority")
	}
	dir, proof, e := exclusiveInstall(root, r, r.StartHome)
	if e != nil {
		return p, e
	}
	defer dir.Close()
	if proof.Removing != "" {
		return p, fmt.Errorf("original installation already has an unresolved uninstall")
	}
	p = UninstallPlan{Schema: UninstallSchema, Key: key, Reason: reason, Transfer: q, Ownership: proof, Stop: stop, Recovery: UninstallRecoveryPath(q, key), Preserved: uninstallPreserved()}
	if e = p.Validate(); e != nil {
		return p, e
	}
	return p, checkPendingUninstall(root, p)
}
func readUninstall(q meshssh.Transfer, key string) (r UninstallReceipt, e error) {
	if e = checkProvisionRequest(q); e != nil {
		return r, e
	}
	if !ValidKey(key) {
		return r, fmt.Errorf("original uninstall key required")
	}
	raw, e := privateFile(uninstallReceiptPath(q, key))
	if e != nil {
		return r, e
	}
	if e = Decode(raw, &r); e != nil {
		return r, e
	}
	h, e := r.Plan.Digest()
	if e != nil || r.Schema != UninstallSchema || r.Plan.Key != key || r.Plan.Transfer != q || r.SHA256 != h || r.Updated.IsZero() {
		return r, fmt.Errorf("original uninstall receipt changed")
	}
	if e = validateNativeUninstallReceipt(r); e != nil {
		return r, e
	}
	return r, nil
}
func saveUninstall(root *os.File, r *UninstallReceipt) error {
	r.Updated = time.Now().UTC()
	raw, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	e = writePrivate(root, filepath.Base(uninstallReceiptPath(r.Plan.Transfer, r.Plan.Key)), append(raw, '\n'), false)
	traceUninstall("receipt-write-return", r.Plan, r, e)
	return e
}

// BeginUninstall retains a private verified helper outside the removable artifact
// before acknowledging admission. Existing keys are read-only, never recopy/retry.
func BeginUninstall(ctx context.Context, p UninstallPlan) (r UninstallReceipt, resultErr error) {
	defer func() { traceUninstall("begin-call-return", p, &r, resultErr) }()
	if e := p.Validate(); e != nil {
		return r, e
	}
	root, unlock, e := provisionExistingLock(p.Transfer)
	if e != nil {
		return r, e
	}
	defer unlock()
	if old, e := readUninstall(p.Transfer, p.Key); e == nil {
		h, _ := p.Digest()
		if old.SHA256 != h {
			return r, fmt.Errorf("uninstall key belongs to another immutable plan")
		}
		return old, nil
	} else if !os.IsNotExist(e) {
		return r, e
	}
	receipt, e := readProvision(p.Transfer)
	if e != nil {
		return r, e
	}
	artifact, proof, e := exclusiveInstall(root, receipt, p.Stop.Plan.Target.Home)
	if e != nil {
		return r, e
	}
	defer artifact.Close()
	if !reflect.DeepEqual(proof, p.Ownership) {
		return r, fmt.Errorf("publication references changed since review")
	}
	if e = checkPendingUninstall(root, p); e != nil {
		return r, e
	}
	hash, _ := p.Digest()
	r = UninstallReceipt{Schema: UninstallSchema, Plan: p, SHA256: hash, Phase: "copy-intent", Detail: "Target admission retained before any recovery allocation; Stop has not been retried."}
	if e = saveUninstall(root, &r); e != nil {
		return r, e
	}
	copyComplete := false
	defer func() {
		if resultErr != nil && !copyComplete {
			r.Phase = "copy-unknown"
			r.Detail = "Original recovery-copy duty unresolved; preserve partial files and inspect this key."
			resultErr = errors.Join(resultErr, saveUninstall(root, &r))
		}
	}()
	name := filepath.Base(filepath.Dir(p.Recovery))
	if e = unix.Mkdirat(int(root.Fd()), name, 0700); e != nil {
		return r, fmt.Errorf("recovery directory exists or could not be created; preserve and inspect: %w", e)
	}
	recovery, e := privateChildDirectory(root, name)
	if e != nil {
		return r, e
	}
	defer recovery.Close()
	source, e := verifyProvisionBinary(artifact, "arm", p.Transfer)
	if e != nil {
		return r, e
	}
	defer source.Close()
	fd, e := unix.Openat(int(recovery.Fd()), "arm", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0700)
	if e != nil {
		return r, e
	}
	out := os.NewFile(uintptr(fd), p.Recovery)
	traceUninstallAllocation(p, r, out)
	// Copy is bounded, context-cancellable, exclusive and never cleaned by guessed
	// names after a partial failure. All remnants remain visible for reconciliation.
	reader := io.NewSectionReader(source, 0, p.Transfer.Size+1)
	buffer := make([]byte, 128<<10)
	var copied int64
	for {
		if e = ctx.Err(); e != nil {
			break
		}
		n, re := reader.Read(buffer)
		if n > 0 {
			var wrote int
			wrote, e = out.Write(buffer[:n])
			copied += int64(wrote)
			if e != nil || wrote != n {
				if e == nil {
					e = io.ErrShortWrite
				}
				break
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			e = re
			break
		}
		if copied > MaxArtifactBytes {
			e = fmt.Errorf("recovery copy exceeds bound")
			break
		}
	}
	if e == nil && copied != p.Transfer.Size {
		e = fmt.Errorf("recovery source changed size")
	}
	if e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return r, e
	}
	if e = recovery.Sync(); e != nil {
		return r, e
	}
	helper, e := verifyProvisionBinary(recovery, "arm", p.Transfer)
	if e != nil {
		return r, e
	}
	id, e := installIdentity(helper)
	helper.Close()
	if e != nil {
		return r, e
	}
	if e = root.Sync(); e != nil {
		return r, e
	}
	h, _ := p.Digest()
	r = UninstallReceipt{Schema: UninstallSchema, Plan: p, SHA256: h, Phase: "accepted", RecoveryIdentity: id, Detail: "Accepted exact uninstall; Stop and removal still require positive original-generation evidence."}
	if e = saveUninstall(root, &r); e != nil {
		return r, e
	}
	copyComplete = true
	return r, nil
}
func MarkUninstallStop(p UninstallPlan, known bool) (UninstallReceipt, error) {
	root, unlock, e := provisionExistingLock(p.Transfer)
	if e != nil {
		return UninstallReceipt{}, e
	}
	defer unlock()
	r, e := readUninstall(p.Transfer, p.Key)
	if e != nil {
		return r, e
	}
	h, _ := p.Digest()
	if h != r.SHA256 {
		return r, fmt.Errorf("uninstall review changed")
	}
	if r.Phase != "accepted" {
		return r, nil
	}
	r.Phase = "stop-unknown"
	if known {
		r.Phase = "stop-admitted"
	}
	return r, saveUninstall(root, &r)
}

// AuthorizeUninstallNative rechecks the destination's actual retained grant.
// Native SSH account access alone never bypasses denied/revoked mesh admission.
func AuthorizeUninstallNative(p UninstallPlan) error { return authorizeUninstallNative(p, false) }
func AuthorizeUninstallRead(p UninstallPlan) error   { return authorizeUninstallNative(p, true) }
func authorizeUninstallNative(p UninstallPlan, readOnly bool) (resultErr error) {
	var c Config
	defer func() {
		action := "auth-mutation-return"
		if readOnly {
			action = "auth-read-return"
		}
		traceUninstallAuthorization(action, p, readOnly, c, resultErr)
	}()
	raw, e := privateFile(filepath.Join(p.Stop.Plan.Target.Home, "mesh", "config.json"))
	if e != nil {
		return e
	}
	if e = Decode(raw, &c); e != nil {
		return e
	}
	target := p.Stop.Plan.Target
	if e = validateStoredPeerRoster(c); e != nil {
		return e
	}
	if e = validateListen(c.Listen); e != nil {
		return e
	}
	if _, e = parseIdentity(c.Identity, true); e != nil {
		return e
	}
	if !readOnly && !c.ListenerEnabled() || c.Identity.Host != target.Host || c.Identity.Owner != target.Owner || c.Identity.Fingerprint != target.Certificate || c.Identity.SPKI != target.SPKI {
		return fmt.Errorf("current destination identity differs")
	}
	actor := p.Stop.Plan.Actor
	for _, peer := range c.Peers {
		if peer.Identity.Host == actor.Host && peer.Identity.Fingerprint == actor.Certificate && peer.Identity.SPKI == actor.SPKI && peer.Pair == actor.Pair && peer.State == "active" && peer.Proven {
			if _, e = parseIdentity(peer.Identity, true); e != nil {
				return e
			}
			if e = peer.Grant.Validate(); e != nil {
				return e
			}
			if readOnly {
				if peer.Grant.Has("node-uninstall") || peer.Grant.Has("observe-own") || peer.Grant.Has("observe-all") {
					return nil
				}
			} else if peer.Revision == actor.GrantRevision && peer.Grant.Has("node-uninstall") && peer.Grant.Has("node-stop") {
				return nil
			}
			return fmt.Errorf("current permission does not authorize this uninstall operation")
		}
	}
	return fmt.Errorf("original destination-local uninstall/Stop grant is unavailable or changed")
}
func LookupUninstall(q meshssh.Transfer, key string) (UninstallReceipt, error) {
	return readUninstall(q, key)
}

// RemoveUninstallArtifact runs only inside the caller's positively inspected,
// still-held native daemon lock. It removes one exact executable, never trees or
// catalogs; the owned directory/ownership receipt and all state remain retained.
func RemoveUninstallArtifact(p UninstallPlan, observation nodestop.Observation) (out UninstallResult, resultErr error) {
	defer func() { traceUninstall("remove-call-return", p, &out.Receipt, resultErr) }()
	root, unlock, e := provisionExistingLock(p.Transfer)
	if e != nil {
		return out, e
	}
	defer unlock()
	r, e := readUninstall(p.Transfer, p.Key)
	out.Receipt = r
	if e != nil {
		return out, e
	}
	h, _ := p.Digest()
	if r.SHA256 != h {
		return out, fmt.Errorf("exact uninstall plan changed")
	}
	stop := p.Stop.Plan
	stopHash, _ := stop.Digest()
	if observation.Record.Validate() != nil || observation.Record.Phase != "drain-ready" || observation.Desired.Target != stop.Target || !observation.ServerJoined || !observation.OriginalGenerationAbsent || observation.Record.Plan.Target != stop.Target || observation.Record.Plan.Actor != stop.Actor || observation.Record.Digest != stopHash || observation.Record.Plan.Key != stop.Key || observation.Desired.Running || observation.Desired.Key != stop.Key || observation.Desired.Digest != stopHash || observation.Desired.Revision != observation.Record.DesiredRevision {
		return out, fmt.Errorf("original Stop is incomplete, superseded or restarted; no removal")
	}
	if e = AuthorizeUninstallNative(p); e != nil {
		return out, e
	}
	out.Stop = &observation
	out.StopObserved = true
	if r.Phase == "removed" {
		out.ArtifactAbsent, e = uninstallBinaryAbsent(p)
		out.Detail = "Original removal receipt; no mutation repeated."
		return out, e
	}
	if r.Phase == "remove-intent" {
		if absent, e := uninstallBinaryAbsent(p); e != nil {
			return out, e
		} else if absent {
			out.ArtifactAbsent = true
			out.Detail = "Executable absence observed after original removal intent; no deletion retried or causal completion invented."
			return out, nil
		}
	}
	receipt, e := readProvision(p.Transfer)
	if e != nil {
		return out, e
	}
	artifact, proof, e := exclusiveInstall(root, receipt, stop.Target.Home)
	traceUninstallOwnership(p, proof, e)
	if e != nil {
		return out, e
	}
	defer artifact.Close()
	if proof.Removing != "" && proof.Removing != p.Key {
		return out, fmt.Errorf("another original uninstall owns removal")
	}
	// Fence only positively stopped removal, not a copy whose Stop may refuse.
	// Registration takes the same provision lock, so no new home can slip between
	// this fresh exclusive-reference check and the durable startup fence.
	if proof.Removing == "" {
		proof.Removing = p.Key
		if e = saveInstallOwnership(artifact, proof, false); e != nil {
			return out, e
		}
	}
	r.Phase = "remove-intent"
	r.Detail = "Exact executable removal intent retained before the sole unlink; state and recovery remain."
	if e = saveUninstall(root, &r); e != nil {
		return out, e
	}
	out.Receipt = r
	if e = AuthorizeUninstallNative(p); e != nil {
		return out, e
	}
	check, e := verifyInstallOwnership(root, artifact, p.Transfer, &p.Ownership)
	traceUninstallOwnership(p, check, e)
	if e != nil || check.Removing != p.Key {
		if e == nil {
			e = fmt.Errorf("uninstall fence changed")
		}
		return out, e
	}
	if e = provisionRootNamed(root, p.Transfer.Root); e != nil {
		return out, e
	}
	e = unix.Unlinkat(int(artifact.Fd()), "arm", 0)
	traceUninstall("unlink-call-return", p, &r, e)
	if e != nil {
		return out, e
	}
	if e = artifact.Sync(); e != nil {
		return out, e
	}
	absent, absenceErr := uninstallBinaryAbsent(p)
	traceUninstallAbsence(p, absent, absenceErr)
	if absenceErr != nil {
		return out, absenceErr
	}
	if !absent {
		return out, fmt.Errorf("original executable removal outcome cannot be established at the reviewed name")
	}
	r.Phase = "removed"
	r.Detail = "Exact ARM-owned executable removed; all data, configuration, ownership and recovery evidence retained."
	if e = saveUninstall(root, &r); e != nil {
		return out, e
	}
	out.Receipt = r
	out.ArtifactAbsent = true
	out.Detail = r.Detail
	return out, nil
}
func uninstallBinaryAbsent(p UninstallPlan) (bool, error) {
	root, e := directory(p.Transfer.Root)
	if e != nil {
		return false, e
	}
	defer root.Close()
	rootID, e := installIdentity(root)
	if e != nil || rootID != p.Ownership.Root {
		return false, fmt.Errorf("original installation root changed")
	}
	dir, e := privateChildDirectory(root, "artifact-"+p.Transfer.SHA256)
	if e != nil {
		return false, e
	}
	defer dir.Close()
	id, e := installIdentity(dir)
	if e != nil || id != p.Ownership.Artifact {
		return false, fmt.Errorf("original artifact namespace identity changed")
	}
	var st unix.Stat_t
	e = unix.Fstatat(int(dir.Fd()), "arm", &st, unix.AT_SYMLINK_NOFOLLOW)
	if e == unix.ENOENT {
		return true, nil
	}
	if e != nil {
		return false, e
	}
	return false, nil
}

// CheckUninstallHelper binds the executing native helper before any callback.
func CheckUninstallHelper(r UninstallReceipt) error {
	runtime, e := CaptureRuntimeExecutable()
	if e != nil {
		return e
	}
	defer runtime.Close()
	proof, e := runtime.Proof()
	if e != nil || proof.SHA256 != r.Plan.Transfer.SHA256 || proof.Size != r.Plan.Transfer.Size {
		return fmt.Errorf("executing recovery image differs")
	}
	actual, e := installIdentity(runtime.file)
	if e != nil || actual != r.RecoveryIdentity {
		return fmt.Errorf("executing recovery inode differs")
	}
	path, e := os.Executable()
	if e != nil || path != r.Plan.Recovery {
		return fmt.Errorf("only the original retained recovery helper may mutate")
	}
	dir, e := directory(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	f, e := verifyProvisionBinary(dir, "arm", r.Plan.Transfer)
	if e != nil {
		return e
	}
	defer f.Close()
	id, e := installIdentity(f)
	if e != nil || id != r.RecoveryIdentity {
		return fmt.Errorf("retained recovery helper was replaced")
	}
	return nil
}

type UninstallPrepare struct {
	Key      string                `json:"key"`
	Reason   string                `json:"reason"`
	Transfer meshssh.Transfer      `json:"installation"`
	Policy   model.ShutdownRequest `json:"policy"`
}
type UninstallApply struct {
	Key    string        `json:"key"`
	Plan   UninstallPlan `json:"plan"`
	SHA256 string        `json:"review_sha256"`
}

func UninstallInstalledPath(q meshssh.Transfer) string {
	return filepath.Join(q.Root, "artifact-"+q.SHA256, "arm")
}
func UninstallMissing(e error) bool { return os.IsNotExist(e) }

func uninstallPreserved() []string {
	return []string{"database", "node identity and grants", "configuration and startup policy", "lease and operation history", "backups and retained artifacts", "companion catalog configuration", "recovery helper and uninstall receipt"}
}

// ObserveUninstall reads current namespace evidence without repairing a receipt
// or inferring the original effect from absence after an unknown reply.
func ObserveUninstall(r UninstallReceipt, observation nodestop.Observation) (UninstallResult, error) {
	p := r.Plan
	out := UninstallResult{Receipt: r, Detail: "Original receipt and current exact-generation observation; no mutation repeated."}
	if observation.Record.Digest != p.Stop.SHA256 || observation.Record.Plan.Target != p.Stop.Plan.Target || observation.Record.Plan.Actor != p.Stop.Plan.Actor || !observation.ServerJoined || !observation.OriginalGenerationAbsent {
		return out, fmt.Errorf("original Stop observation differs")
	}
	out.StopObserved = true
	out.Stop = &observation
	if r.Phase == "removed" || r.Phase == "remove-intent" {
		var e error
		out.ArtifactAbsent, e = uninstallBinaryAbsent(p)
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

type UninstallLookupRequest struct {
	Key      string           `json:"key"`
	Transfer meshssh.Transfer `json:"installation"`
}

// Pending copy duties are distinct from the removal/startup fence. They retain
// bounded target allocation ownership through crashes and prohibit new-key copies.
func checkPendingUninstall(root *os.File, p UninstallPlan) error {
	entries, e := installRootEntries(root, p.Transfer.Root)
	if e != nil {
		return e
	}
	if len(entries)+2 > 512 {
		return fmt.Errorf("install namespace has no bounded admission slots")
	}
	count := 0
	pattern := regexp.MustCompile(`^uninstall-[a-f0-9]{64}\.json$`)
	for _, entry := range entries {
		if !pattern.MatchString(entry.Name()) {
			continue
		}
		count++
		raw, e := installPrivateAt(root, entry.Name())
		if e != nil {
			return e
		}
		var candidate UninstallReceipt
		if e = Decode(raw, &candidate); e != nil {
			return e
		}
		if entry.Name() != filepath.Base(uninstallReceiptPath(candidate.Plan.Transfer, candidate.Plan.Key)) || candidate.Plan.Transfer.Root != p.Transfer.Root {
			return fmt.Errorf("invalid uninstall admission filename/root")
		}
		prior, e := readUninstall(candidate.Plan.Transfer, candidate.Plan.Key)
		if e != nil {
			return e
		}
		if prior.Plan.Transfer.SHA256 == p.Transfer.SHA256 && prior.Phase != "removed" {
			return fmt.Errorf("original pending uninstall %q owns this artifact; inspect that key, never allocate another copy", prior.Plan.Key)
		}
	}
	if count >= 128 {
		return fmt.Errorf("bounded uninstall history is full; no recovery allocation admitted")
	}
	return nil
}

func validateNativeUninstallReceipt(r UninstallReceipt) error {
	if r.Updated.IsZero() || len(r.Detail) > 4096 {
		return fmt.Errorf("invalid native receipt update/detail")
	}
	switch r.Phase {
	case "copy-intent":
		if r.RecoveryIdentity != (model.FileIdentity{}) {
			return fmt.Errorf("copy intent has unexplained completed identity")
		}
	case "copy-unknown":
		if r.RecoveryIdentity != (model.FileIdentity{}) && !r.RecoveryIdentity.BirthKnown {
			return fmt.Errorf("partial recovery identity")
		}
	case "accepted", "stop-admitted", "stop-unknown", "remove-intent", "removed":
		if !r.RecoveryIdentity.BirthKnown {
			return fmt.Errorf("recovery copy has no original native identity")
		}
	default:
		return fmt.Errorf("unknown uninstall phase")
	}
	return nil
}
