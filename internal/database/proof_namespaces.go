package database

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/storage"
	"golang.org/x/sys/unix"
)

type NamespaceEvidence struct {
	Name       string                    `json:"name"`
	Present    bool                      `json:"present"`
	Identity   File                      `json:"identity"`
	Files      []File                    `json:"files"`
	SSHHistory *SSHSourceHistoryEvidence `json:"ssh_source_history,omitempty"`
}
type MeshRetirement struct {
	Host        string `json:"host"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Peers       int    `json:"peer_records"`
	Required    bool   `json:"explicit_retirement_required"`
	Scope       string `json:"scope"`
}

// SettledReview holds the stopped daemon authority and existing coordination
// locks while evidence is reviewed. It does not create absent locks/namespaces.
// Missing locks are exact absence observations, never permission to mutate.
type SettledReview struct {
	forgetTrees []ForgetTree
	Proof       SettledProof        `json:"proof"`
	Namespaces  []NamespaceEvidence `json:"namespaces"`
	Mesh        *MeshRetirement     `json:"mesh,omitempty"`
	source      *Source
	locks       []*os.File
	directories []*Directory
}

func (r *SettledReview) Close() error {
	var result error
	for _, f := range r.locks {
		if e := unix.Flock(int(f.Fd()), unix.LOCK_UN); e != nil {
			result = e
		}
		if e := f.Close(); e != nil {
			result = e
		}
	}
	for _, d := range r.directories {
		if e := d.Close(); e != nil {
			result = e
		}
	}
	if r.source != nil {
		if e := r.source.Close(); e != nil {
			result = e
		}
	}
	return result
}

// ReviewSettled is the read-only full namespace checkpoint. No CLI deletion or
// irreversible state machine is wired to it. Even a successful review must be
// revalidated under the eventual confirmed apply protocol before any change.
func ReviewSettled(ctx context.Context, home, owner string) (review *SettledReview, err error) {
	ctx, cancel := context.WithTimeout(ctx, FullTimeout)
	defer cancel()
	if err = local.CheckStateFence(home); err != nil {
		return nil, err
	}
	r := &SettledReview{}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	r.source, err = Offline(ctx, home)
	if err != nil {
		return nil, err
	}
	// Fixed lock order: daemon (Source), lifecycle, then dashboard pending.
	if err = r.existingLock(r.source.Directory, "mesh-lifecycle.lock"); err != nil {
		return nil, err
	}
	pending, err := r.child("dashboard-pending")
	if err != nil {
		return nil, err
	}
	if pending != nil {
		if err = r.existingLock(pending, ".lock"); err != nil {
			return nil, err
		}
	}
	r.Proof, err = ProveDatabaseSettled(ctx, r.source, owner)
	if err != nil {
		return nil, err
	}
	if err = r.storageRoot(); err != nil {
		return nil, err
	}
	if pending != nil {
		files, _, e := r.readNamespace(ctx, pending, 64<<10, 65)
		if e != nil {
			return nil, e
		}
		for _, f := range files {
			if f.Name != ".lock" {
				if len(r.Proof.Blockers) >= MaxProofBlockers {
					return nil, fmt.Errorf("too many proof blockers")
				}
				r.Proof.Blockers = append(r.Proof.Blockers, ProofBlocker{"client_intent", f.Name, "A retained pending/unknown client operation must be settled; no record was deleted"})
			}
		}
	}
	lifecycle, e := r.child("mesh-lifecycle")
	if e != nil {
		return nil, e
	}
	if lifecycle != nil {
		coordinators := map[string][]byte{}
		lifecycleRecords := map[string]mesh.LifecycleOperation{}
		_, contents, e := r.readNamespace(ctx, lifecycle, MaxRecordBytes, 512)
		if e != nil {
			return nil, e
		}
		for _, name := range sortedKeys(contents) {
			raw := contents[name]
			if strings.HasPrefix(name, "coordinator-") {
				coordinators[name] = raw
				continue
			}
			var op mesh.LifecycleOperation
			if e = strictEvidence(string(raw), &op, "actor", "transitions", "key", "kind", "request_sha256", "at", "updated_at", "phase", "reason"); e != nil {
				return nil, e
			}
			path, e := mesh.LifecycleOperationPath(home, op.Key)
			if e != nil || filepath.Base(path) != name || op.Actor.UID != owner || !oneOf(op.Kind, "connect", "reconnect", "disconnect") || op.At.IsZero() || op.Updated.Before(op.At) || len(op.Transitions) == 0 || op.Transitions[len(op.Transitions)-1].Phase != op.Phase || op.LocalHost != "" && op.LocalHost != r.Proof.Host {
				return nil, fmt.Errorf("invalid lifecycle operation identity/authority")
			}
			if !oneOf(op.Phase, "connected", "disconnected") {
				r.Proof.Blockers = append(r.Proof.Blockers, ProofBlocker{"mesh_lifecycle", op.Key, "Peer setup/disconnection has not durably settled"})
			}
			lifecycleRecords[op.Key] = op
		}
		blockers, err := proveResetCoordinations(coordinators, owner, r.Proof.Host, lifecycleRecords)
		if err != nil {
			return nil, err
		}
		r.Proof.Blockers = append(r.Proof.Blockers, blockers...)
		if len(coordinators) > 0 {
			r.Mesh = &MeshRetirement{Host: r.Proof.Host, Required: true, Scope: "Retain/archive local coordinator and lifecycle history with explicit mesh retirement. This is not evidence of remote cleanup or resource release."}
		}
	}
	configured, e := r.child("mesh")
	if e != nil {
		return nil, e
	}
	if configured != nil {
		files, contents, e := r.readNamespace(ctx, configured, MaxRecordBytes, 256)
		if e != nil {
			return nil, e
		}
		if configured.identity.Mode&07777 != 0700 {
			return nil, fmt.Errorf("mesh/SSH namespace must retain exact0700 permissions")
		}
		for _, file := range files {
			if file.Mode&07777 != 0600 {
				return nil, fmt.Errorf("mesh/SSH namespace records must retain exact0600 permissions")
			}
		}
		if contents["config.json"] == nil || contents["node-key.pem"] == nil {
			return nil, fmt.Errorf("mesh namespace lacks complete known config/key pair")
		}
		var config mesh.Config
		if e = strictEvidence(string(contents["config.json"]), &config, "identity", "listen", "peers"); e != nil {
			return nil, e
		}
		if config.Identity.Host != r.Proof.Host || config.Identity.Owner != owner {
			return nil, fmt.Errorf("mesh identity differs from stopped ledger authority")
		}
		cert, e := tls.X509KeyPair([]byte(config.Identity.Certificate), contents["node-key.pem"])
		if e != nil || len(cert.Certificate) != 1 {
			return nil, fmt.Errorf("mesh key/certificate evidence does not match")
		}
		if e = mesh.ValidateRegistry(config); e != nil {
			return nil, fmt.Errorf("invalid stopped mesh registry evidence: %w", e)
		}

		sshBlockers, e := proveSSHNamespace(ctx, contents, r.Proof.Host, owner)
		if e != nil {
			return nil, e
		}
		r.Proof.Blockers = append(r.Proof.Blockers, sshBlockers...)
		if e = r.reviewSSHSourceHistory(ctx, configured, contents); e != nil {
			return nil, e
		}

		r.Mesh = &MeshRetirement{Host: config.Identity.Host, Name: config.Identity.Name, Fingerprint: config.Identity.Fingerprint, Peers: len(config.Peers), Required: true, Scope: "Local archive/identity retirement requires separate explicit review; no remote revoke or claim cleanup. Destination-owned claims retain original authority and expiry."}
	}
	if configured == nil {
		if e = r.reviewSSHSourceHistory(ctx, nil, nil); e != nil {
			return nil, e
		}
	}
	if err = r.Validate(ctx); err != nil {
		return nil, err
	}
	if len(r.Proof.Blockers) > MaxProofBlockers {
		return nil, fmt.Errorf("external proof exceeds complete blocker bound")
	}
	combined, _ := json.Marshal([]any{r.Proof.SHA256, r.Namespaces, r.Mesh})
	sum := sha256.Sum256(combined)
	r.Proof.SHA256 = hex.EncodeToString(sum[:])
	r.Proof.Settled = len(r.Proof.Blockers) == 0
	r.Proof.Scope = "Complete current-schema database plus bounded pending/lifecycle/mesh/storage namespace review under stopped authority. Configured mesh additionally requires explicit retirement. Read-only evidence only; no deletion authorized or performed."
	return r, nil
}
func (r *SettledReview) existingLock(d *Directory, name string) error {
	f, v, e := d.Open(name, unix.O_RDONLY)
	if e != nil {
		return e
	}
	if f == nil {
		r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: filepath.Join(d.Path, name), Files: []File{v}})
		return nil
	}
	if v.Size != 0 {
		f.Close()
		return fmt.Errorf("coordination lock must be empty")
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return fmt.Errorf("maintenance coordination is busy: %w", e)
	}
	named, e := d.Observe(name, false)
	if e != nil || !sameIdentity(v, named) {
		f.Close()
		return fmt.Errorf("coordination lock identity changed")
	}
	r.locks = append(r.locks, f)
	r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: filepath.Join(d.Path, name), Present: true, Identity: v, Files: []File{v}})
	return nil
}
func (r *SettledReview) child(name string) (*Directory, error) {
	parent := r.source.Directory
	fd, e := unix.Openat(int(parent.FD.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e == unix.ENOENT {
		r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: name, Files: []File{}})
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), filepath.Join(parent.Path, name))
	id, e := nativeFile(f, name, true)
	if e != nil {
		f.Close()
		return nil, e
	}
	d := &Directory{Path: filepath.Join(parent.Path, name), FD: f, identity: id}
	r.directories = append(r.directories, d)
	return d, nil
}
func (r *SettledReview) readNamespace(ctx context.Context, d *Directory, limit int64, count int) ([]File, map[string][]byte, error) {
	// Open a separate directory description; Readdir on dup would share offsets.
	fd, e := unix.Openat(int(d.FD.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, nil, e
	}
	f := os.NewFile(uintptr(fd), d.Path)
	names, e := f.Readdirnames(count + 1)
	f.Close()
	if e != nil && e != io.EOF {
		return nil, nil, e
	}
	if len(names) > count {
		return nil, nil, fmt.Errorf("%s exceeds complete %d-entry proof bound", filepath.Base(d.Path), count)
	}
	sort.Strings(names)
	files := []File{}
	contents := map[string][]byte{}
	for _, name := range names {
		if !evidenceID(name) {
			return nil, nil, fmt.Errorf("unsafe or oversized namespace entry name")
		}
		if e = ctx.Err(); e != nil {
			return nil, nil, e
		}
		source, id, e := d.Open(name, unix.O_RDONLY)
		if e != nil || source == nil {
			if e == nil {
				e = fmt.Errorf("namespace entry disappeared")
			}
			return nil, nil, e
		}
		if id.Size > MaxProofBytes-r.Proof.EvidenceBytes {
			source.Close()
			return nil, nil, fmt.Errorf("namespace evidence exceeds complete 64 MiB proof bound")
		}
		r.Proof.EvidenceBytes += id.Size
		if id.Size > limit {
			source.Close()
			return nil, nil, fmt.Errorf("namespace evidence exceeds record bound")
		}
		raw, e := io.ReadAll(io.LimitReader(source, limit+1))
		source.Close()
		if e != nil || int64(len(raw)) != id.Size {
			return nil, nil, fmt.Errorf("namespace evidence changed while reading")
		}
		sum := sha256.Sum256(raw)
		id.SHA256 = hex.EncodeToString(sum[:])
		files = append(files, id)
		contents[name] = raw
	}
	r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: filepath.Base(d.Path), Present: true, Identity: d.identity, Files: files})
	return files, contents, nil
}
func (r *SettledReview) storageRoot() error {
	d, err := r.child("storage")
	if err != nil {
		return err
	}
	expected := r.Proof.StorageRoot
	if d == nil {
		if expected != nil {
			return fmt.Errorf("recorded storage infrastructure root is missing")
		}
		return nil
	}
	if expected == nil {
		return fmt.Errorf("unrecorded storage infrastructure root cannot be adopted")
	}
	id, err := storage.DirectoryIdentity(int(d.FD.Fd()))
	if err != nil {
		return err
	}
	if id != *expected {
		return fmt.Errorf("storage infrastructure root identity changed; no child was adopted")
	}
	if err := r.verifyRemovedStorage(d); err != nil {
		return err
	}
	r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: "storage", Present: true, Identity: d.identity, Files: []File{}})
	return nil
}
func (r *SettledReview) verifyRemovedStorage(d *Directory) error {
	for _, name := range r.Proof.removedStorage {
		for _, candidate := range []string{name, ".reclaim-" + name} {
			var st unix.Stat_t
			e := unix.Fstatat(int(d.FD.Fd()), candidate, &st, unix.AT_SYMLINK_NOFOLLOW)
			if e != unix.ENOENT {
				return fmt.Errorf("storage recorded removed but namespace remains or cannot be verified")
			}
		}
	}
	return nil
}
func (r *SettledReview) Validate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, FullTimeout)
	defer cancel()
	if err := r.source.Validate(ctx, true); err != nil {
		return err
	}
	for _, tree := range r.forgetTrees {
		current, e := scanForgetTree(ctx, r.source.Directory, tree.Name)
		if e != nil {
			return e
		}
		if !sameForgetTree(current, tree) {
			return fmt.Errorf("reviewed forget bookkeeping changed")
		}
	}
	for _, n := range r.Namespaces {
		if err := validSSHHistoryEvidence(n); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := n.Name
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.source.Directory.Path, path)
		}
		if !n.Present {
			if _, e := os.Lstat(path); !os.IsNotExist(e) {
				return fmt.Errorf("previously absent review namespace appeared: %s", filepath.Base(path))
			}
			continue
		}
		if n.Name == "ssh-source-history" {
			if err := checkSSHHistoryEvidencePath(ctx, path, n); err != nil {
				return err
			}
			continue
		}
		if n.Identity.Mode&unix.S_IFMT == unix.S_IFDIR {
			d, e := OpenDirectory(path)
			if e != nil {
				return e
			}
			if r.source.opaque {
				if e := requireForgetMount(d.FD, r.source.mount, forgetMountIdentity); e != nil {
					d.Close()
					return e
				}
			}
			if !sameIdentity(d.identity, n.Identity) {
				d.Close()
				return fmt.Errorf("reviewed namespace changed")
			}
			if n.Name == "storage" {
				if e := r.verifyRemovedStorage(d); e != nil {
					d.Close()
					return e
				}
			}
			if n.Name != "storage" {
				fd, e := unix.Openat(int(d.FD.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if e != nil {
					d.Close()
					return e
				}
				f := os.NewFile(uintptr(fd), path)
				names, e := f.Readdirnames(len(n.Files) + 1)
				f.Close()
				if e != nil && e != io.EOF || len(names) != len(n.Files) {
					d.Close()
					return fmt.Errorf("review namespace membership changed")
				}
				for _, v := range n.Files {
					f, current, e := d.Open(v.Name, unix.O_RDONLY)
					if e != nil || f == nil {
						d.Close()
						return fmt.Errorf("review file disappeared or became unsafe")
					}
					if r.source.opaque {
						if e := requireForgetMount(f, r.source.mount, forgetMountIdentity); e != nil {
							f.Close()
							d.Close()
							return e
						}
					}
					hash, e := hashFile(ctx, f)
					f.Close()
					if e != nil || (!sameIdentity(v, current) || v.Size != current.Size || v.Modified != current.Modified || v.Changed != current.Changed) || hash != v.SHA256 {
						d.Close()
						return fmt.Errorf("review namespace file changed")
					}
				}
			}
			d.Close()
		} else {
			parent, e := OpenDirectory(filepath.Dir(path))
			if e != nil {
				return e
			}
			current, e := parent.Observe(filepath.Base(path), false)
			parent.Close()
			if e != nil || !sameIdentity(n.Identity, current) {
				return fmt.Errorf("review lock identity changed")
			}
		}
	}
	return nil
}
