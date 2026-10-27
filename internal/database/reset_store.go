package database

import (
	"bytes"
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
	"sort"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"golang.org/x/sys/unix"
)

const resetReceiptName = "receipt.json"
const resetFreshName = "fresh.db"
const maxResetReceipts = 64

// resetStore owns only its exact operation directory. It never recursively
// deletes the state directory, an archive, or an unverified temporary child.
type resetStore struct {
	forgetMount                string
	home, container, operation *Directory
}

func (s *resetStore) close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.operation != nil {
		err = errors.Join(err, s.operation.Close())
	}
	if s.container != nil {
		err = errors.Join(err, s.container.Close())
	}
	if s.home != nil {
		err = errors.Join(err, s.home.Close())
	}
	return err
}
func resetChild(parent *Directory, name string, create bool) (*Directory, error) {
	if !safeName(name) {
		return nil, fmt.Errorf("exact private child name required")
	}
	if create {
		if err := unix.Mkdirat(int(parent.FD.Fd()), name, 0700); err != nil && err != unix.EEXIST {
			return nil, err
		}
		if err := parent.Sync(); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(parent.FD.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), filepath.Join(parent.Path, name))
	identity, err := nativeFile(f, name, true)
	if err != nil {
		f.Close()
		return nil, err
	}
	d := &Directory{Path: filepath.Join(parent.Path, name), FD: f, identity: identity}
	if err = d.Validate(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
func openResetStore(home, id string, create bool) (s *resetStore, err error) {
	if !validHost(id) {
		return nil, fmt.Errorf("one exact reset operation ID required")
	}
	s = &resetStore{}
	opened := s
	defer func() {
		if err != nil {
			opened.close()
		}
	}()
	s.home, err = OpenDirectory(home)
	if err != nil {
		return nil, err
	}
	s.container, err = resetChild(s.home, ResetDirectoryName, create)
	if err != nil {
		return nil, err
	}
	names, err := resetNames(s.container, maxResetReceipts)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "reset-") || !validHost(strings.TrimPrefix(name, "reset-")) {
			return nil, fmt.Errorf("unrecognized maintenance evidence retained; no entry was deleted")
		}
	}
	operation := "reset-" + id
	found := false
	for _, name := range names {
		if name == operation {
			found = true
		}
	}
	if create && found {
		return nil, fmt.Errorf("reset operation already exists; use explicit continuation")
	}
	if create && len(names) >= maxResetReceipts {
		return nil, fmt.Errorf("reset receipt history is full; no receipt was removed")
	}
	s.operation, err = resetChild(s.container, operation, create)
	return s, err
}
func resetNames(d *Directory, limit int) ([]string, error) {
	fd, err := unix.Openat(int(d.FD.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), d.Path)
	defer f.Close()
	names, err := f.Readdirnames(limit + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) > limit {
		return nil, fmt.Errorf("maintenance namespace exceeds its complete %d-entry bound", limit)
	}
	sort.Strings(names)
	return names, nil
}
func readResetJSON[T any](d *Directory, name string) (value T, file File, err error) {
	f, file, err := d.Open(name, unix.O_RDONLY)
	if err != nil {
		return value, file, err
	}
	if f == nil {
		return value, file, os.ErrNotExist
	}
	defer f.Close()
	if file.Size > MaxRecordBytes {
		return value, file, fmt.Errorf("maintenance receipt exceeds 1 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxRecordBytes+1))
	if err != nil || int64(len(raw)) != file.Size {
		return value, file, fmt.Errorf("maintenance receipt changed while reading")
	}
	if err = preflightResetCollections(raw); err != nil {
		return value, file, err
	}
	if err = strictEvidence(string(raw), &value); err != nil {
		return value, file, err
	}
	sum := sha256.Sum256(raw)
	file.SHA256 = hex.EncodeToString(sum[:])
	return value, file, nil
}

// atomicResetJSON checks the exact prior inode and bytes before replacement and fsyncs
// file+directory. If publication may already have happened, callers retain the
// journal/fence and report an uncertain result instead of rolling it back.
func atomicResetJSON(d *Directory, name string, value any, expected *File) (err error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > MaxRecordBytes {
		return fmt.Errorf("maintenance receipt exceeds 1 MiB")
	}
	previous, err := observeResetJSONFile(d, name)
	if err != nil {
		return err
	}
	if expected == nil {
		if previous.Present {
			return fmt.Errorf("maintenance destination already exists")
		}
	} else if !sameResetJSONFile(previous, *expected) {
		return fmt.Errorf("maintenance receipt identity or bytes changed")
	}
	temporary := ".reset-write-" + local.ID()
	f, created, err := d.Open(temporary, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY)
	if err != nil {
		return err
	}
	defer func() {
		current, e := d.Observe(temporary, false)
		if e == nil && sameIdentity(created, current) {
			_ = unix.Unlinkat(int(d.FD.Fd()), temporary, 0)
		}
	}()
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = d.Validate(); err != nil {
		return err
	}
	current, err := observeResetJSONFile(d, name)
	if err != nil || !sameResetJSONFile(previous, current) {
		return fmt.Errorf("maintenance destination identity or bytes changed before publication")
	}
	if expected == nil {
		err = renameExclusive(int(d.FD.Fd()), temporary, name)
	} else {
		err = unix.Renameat(int(d.FD.Fd()), temporary, int(d.FD.Fd()), name)
	}
	if err != nil {
		return err
	}
	return d.Sync()
}

// observeResetJSONFile bounds hashing independently of the database-file bound.
// Expected publication identities come from readResetJSON and include SHA256.
func observeResetJSONFile(d *Directory, name string) (File, error) {
	f, file, err := d.Open(name, unix.O_RDONLY)
	if err != nil || f == nil {
		return file, err
	}
	defer f.Close()
	if file.Size > MaxRecordBytes {
		return file, fmt.Errorf("maintenance receipt exceeds 1 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxRecordBytes+1))
	if err != nil || int64(len(raw)) != file.Size {
		return file, fmt.Errorf("maintenance receipt changed while hashing")
	}
	sum := sha256.Sum256(raw)
	file.SHA256 = hex.EncodeToString(sum[:])
	current, err := d.Observe(name, false)
	if err != nil || !sameIdentity(file, current) || file.Size != current.Size || file.Modified != current.Modified || file.Changed != current.Changed {
		return file, fmt.Errorf("maintenance receipt changed while hashing")
	}
	return file, nil
}
func sameResetJSONFile(a, b File) bool {
	return sameIdentity(a, b) && a.Size == b.Size && (!a.Present || validDigest(a.SHA256) && a.SHA256 == b.SHA256)
}
func planDigest(plan ResetPlan) (string, error) {
	plan.SHA256 = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func validResetPlan(plan ResetPlan, home string) error {
	encoded, encodeErr := json.Marshal(plan)
	if encodeErr != nil || len(encoded) > MaxRecordBytes {
		return fmt.Errorf("complete reset plan exceeds 1 MiB")
	}
	if err := validForgetTrees(plan); err != nil {
		return err
	}
	uid := local.CurrentIdentity().UID
	if plan.Schema != ResetSchema || !validHost(plan.ID) || plan.Home != home || plan.Actor.UID != uid || strings.TrimSpace(plan.Reason) == "" || len(plan.Reason) > 4096 || plan.Created.IsZero() || !validHost(plan.NewHost) || plan.OldHost == plan.NewHost {
		return fmt.Errorf("invalid reset plan authority/settled scope")
	}
	if plan.Forget {
		if plan.OldHost != "" || plan.StorageRoot != nil || !reflect.DeepEqual(plan.Proof, forgetProof(uid, plan.Created)) {
			return fmt.Errorf("forget plan must explicitly retain unproved scope and no adopted storage authority")
		}
	} else if !validHost(plan.OldHost) || !plan.Proof.Complete || !plan.Proof.Settled || plan.Proof.Host != plan.OldHost || plan.Proof.Owner != uid || len(plan.Proof.Blockers) != 0 {
		return fmt.Errorf("invalid reset plan authority/settled scope")
	}
	names := resetDatabaseNames(plan)
	if len(plan.Files) != len(names) || plan.Mesh != nil && !plan.RetireMesh {
		return fmt.Errorf("reset plan file or mesh retirement scope incomplete")
	}
	if plan.PriorSeed.Name != ResetSeedName {
		return fmt.Errorf("reset plan lacks exact prior authority pointer scope")
	}
	for _, namespace := range plan.Namespaces {
		if namespace.Present && oneOf(namespace.Name, "mesh", "mesh-lifecycle", "ssh-source-history") && !plan.RetireMesh {
			return fmt.Errorf("mesh evidence requires explicit local retirement")
		}
	}
	for i, f := range plan.Files {
		if f.Name != names[i] || i == 0 && !f.Present || f.Present && (f.Owner != uint32Owner(uid) || f.Mode&unix.S_IFMT != unix.S_IFREG || f.Mode&0022 != 0 || !validDigest(f.SHA256)) {
			return fmt.Errorf("invalid exact reset database file scope")
		}
	}
	if !reflect.DeepEqual(plan.StorageRoot, plan.Proof.StorageRoot) {
		return fmt.Errorf("reset storage seed differs from verified infrastructure identity")
	}
	digest, err := planDigest(plan)
	if err != nil {
		return err
	}
	if digest != plan.SHA256 {
		return fmt.Errorf("reset plan digest changed")
	}
	return nil
}
func validDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}
func (s *resetStore) readReceipt() (ResetReceipt, File, error) {
	r, file, err := readResetJSON[ResetReceipt](s.operation, resetReceiptName)
	if err != nil {
		return r, file, err
	}
	if err = validResetPlan(r.Plan, s.home.Path); err != nil {
		return r, file, err
	}
	if r.Schema != ResetSchema || r.Revision < 1 || r.Updated.Before(r.Plan.Created) || filepath.Base(s.operation.Path) != "reset-"+r.Plan.ID || !oneOf(r.Phase, "staged", "fenced", "moving", "retiring", "seed_ready", "erasing", "completed") {
		return r, file, fmt.Errorf("invalid reset receipt schema/phase")
	}
	if r.Step != "" && !oneOf(r.Step, "state.db", "state.db-wal", "state.db-shm", "mesh", "mesh-lifecycle", "ssh-source-history", "seed", "fence") && !(r.Plan.Forget && (r.Step == "state.db-journal" || validForgetStep(r.Plan, r.Step))) {
		return r, file, fmt.Errorf("invalid reset receipt step")
	}
	if err := validForgetReceipt(r); err != nil {
		return r, file, err
	}
	if r.Plan.Forget && r.ForgetErased == nil {
		r.ForgetErased = map[string]bool{}
	}
	if r.Moves == nil || len(r.Moves) != len(resetDatabaseNames(r.Plan)) || r.Archives == nil {
		return r, file, fmt.Errorf("reset receipt complete move scope missing")
	}
	for i, name := range resetDatabaseNames(r.Plan) {
		if !oneOf(r.Moves[name], "source", "absent", "quarantined", "erased") {
			return r, file, fmt.Errorf("unknown database move state")
		}
		if r.Plan.Files[i].Present == (r.Moves[name] == "absent") {
			return r, file, fmt.Errorf("reset file presence contradicts recorded move scope")
		}
	}
	for name, state := range r.Archives {
		if !oneOf(name, "mesh", "mesh-lifecycle", "ssh-source-history") || !oneOf(state, "source", "archived") {
			return r, file, fmt.Errorf("unknown retained archive state")
		}
	}
	expectedArchives := 0
	for _, namespace := range r.Plan.Namespaces {
		if oneOf(namespace.Name, "mesh", "mesh-lifecycle", "ssh-source-history") && namespace.Present {
			expectedArchives++
			if _, ok := r.Archives[namespace.Name]; !ok {
				return r, file, fmt.Errorf("reset receipt lost reviewed mesh retirement scope")
			}
		}
	}
	if len(r.Archives) != expectedArchives {
		return r, file, fmt.Errorf("reset retirement scope differs from exact plan")
	}
	if len(r.Protected) > 0 || r.Phase != "staged" {
		if err = validResetProtected(r.Plan, r.Protected); err != nil {
			return r, file, err
		}
	}
	if r.Phase == "completed" && (r.Completed == nil || r.Completed.IsZero()) {
		return r, file, fmt.Errorf("reset completion time missing")
	}
	if r.Seed.Present && (r.Seed.Name != resetFreshName || r.Seed.Mode&unix.S_IFMT != unix.S_IFREG || r.Seed.Mode&0077 != 0 || r.Seed.Owner != uint32Owner(r.Plan.Actor.UID)) {
		return r, file, fmt.Errorf("invalid fresh database construction identity")
	}
	if r.SeedReady && (!r.Seed.Present || !validDigest(r.Seed.SHA256) || r.Seed.Size <= 0) {
		return r, file, fmt.Errorf("fresh database readiness lacks exact durable identity")
	}
	if r.Phase != "staged" && !r.SeedReady {
		return r, file, fmt.Errorf("fenced reset has no durably prepared authority")
	}
	if r.Phase == "completed" {
		if !r.SeedReady || r.Step != "" {
			return r, file, fmt.Errorf("completed reset lacks prepared authority or retains a step")
		}
		for _, state := range r.Moves {
			if state != "absent" && state != "erased" {
				return r, file, fmt.Errorf("completed reset still retains database move obligations")
			}
		}
		for _, state := range r.Archives {
			if state != "archived" {
				return r, file, fmt.Errorf("completed reset still retains mesh retirement obligations")
			}
		}
	}
	return r, file, nil
}
func (s *resetStore) saveReceipt(r ResetReceipt, previous *File) error {
	r.Updated = time.Now().UTC()
	return atomicResetJSON(s.operation, resetReceiptName, r, previous)
}
func (s *resetStore) validate() error {
	return errors.Join(s.home.Validate(), s.container.Validate(), s.operation.Validate())
}
func exactResetFile(ctx context.Context, d *Directory, expected File, source bool) (File, error) {
	var f *os.File
	var current File
	var err error
	if source {
		f, current, err = d.openSource(expected.Name)
	} else {
		f, current, err = d.Open(expected.Name, unix.O_RDONLY)
	}
	if err != nil {
		return current, err
	}
	if f == nil {
		if expected.Present {
			return current, os.ErrNotExist
		}
		return current, nil
	}
	defer f.Close()
	if !sameIdentity(current, expected) || current.Size != expected.Size {
		return current, fmt.Errorf("reviewed reset file identity/size changed")
	}
	sum, err := hashFile(ctx, f)
	if err != nil {
		return current, err
	}
	if sum != expected.SHA256 {
		return current, fmt.Errorf("reviewed reset file bytes changed")
	}
	current.SHA256 = sum
	return current, nil
}
func sameResetJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
