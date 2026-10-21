package install

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
)

type BackupEntry struct {
	Source      string    `json:"source"`
	SourceBytes []byte    `json:"source_bytes,omitempty"`
	Exists      bool      `json:"existed"`
	Saved       string    `json:"saved_file,omitempty"`
	Metadata    string    `json:"metadata"`
	SHA256      string    `json:"sha256,omitempty"`
	Mode        *uint32   `json:"mode,omitempty"`
	Identity    string    `json:"identity,omitempty"`
	Modified    time.Time `json:"modified_at,omitzero"`
	Size        int64     `json:"size"`
	UID         *uint32   `json:"uid,omitempty"`
	GID         *uint32   `json:"gid,omitempty"`
	Operation   string    `json:"operation"`
	Created     time.Time `json:"created_at"`
}

func syncRoot(r *os.Root) error {
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func backupWrite(r *os.Root, name string, b []byte) error {
	f, e := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}
func (s *snapshot) backup(home string) (entries []BackupEntry, err error) {
	defer func() {
		if err != nil {
			err = model.Err("backup_failed", "instruction files were not changed: "+err.Error())
		}
	}()
	r, e := os.OpenRoot(home)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if e = r.Mkdir("install-backups", 0700); e != nil && !os.IsExist(e) {
		return nil, e
	}
	info, e := r.Lstat("install-backups")
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("backup root must be a private owned directory, not a symlink")
	}
	parent, e := r.OpenRoot("install-backups")
	if e != nil {
		return nil, e
	}
	defer parent.Close()
	actual, e := parent.Stat(".")
	if e != nil {
		return nil, e
	}
	if fileIdentity(actual) != fileIdentity(info) {
		return nil, fmt.Errorf("backup root changed")
	}
	if e = parent.Mkdir(s.backupID, 0700); e != nil {
		return nil, e
	}
	dest, e := parent.OpenRoot(s.backupID)
	if e != nil {
		return nil, e
	}
	defer dest.Close()
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		want := s.files[path]
		name := digest([]byte(path))
		entry := BackupEntry{Source: path, Exists: want.Exists, Identity: want.Identity, Operation: s.backupOperation, Created: time.Now().UTC(), Metadata: filepath.Join(home, "install-backups", s.backupID, name+".json")}
		if !utf8.ValidString(path) {
			entry.SourceBytes = []byte(path)
		}
		if want.Exists {
			root, e := os.OpenRoot(filepath.Dir(path))
			if e != nil {
				return entries, e
			}
			file, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			root.Close()
			if e != nil {
				return entries, e
			}
			before, e := file.Stat()
			if e != nil {
				file.Close()
				return entries, e
			}
			if !before.Mode().IsRegular() || fileIdentity(before) != want.Identity || before.Mode() != want.Mode {
				file.Close()
				return entries, fmt.Errorf("instruction identity changed before backup")
			}
			saved, e := dest.OpenFile(name+".original", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				file.Close()
				return entries, e
			}
			hash := sha256.New()
			e = saved.Chmod(0600)
			if e == nil {
				_, e = io.Copy(io.MultiWriter(saved, hash), file)
			}
			after, se := file.Stat()
			ce := file.Close()
			if e == nil {
				e = se
			}
			if e == nil {
				e = ce
			}
			if e == nil && (fileIdentity(after) != want.Identity || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || fmt.Sprintf("%x", hash.Sum(nil)) != want.Hash) {
				e = fmt.Errorf("instruction content changed during backup")
			}
			if e == nil {
				e = saved.Sync()
			}
			ce = saved.Close()
			if e == nil {
				e = ce
			}
			if e != nil {
				return entries, e
			}
			entry.Saved = filepath.Join(home, "install-backups", s.backupID, name+".original")
			entry.SHA256 = want.Hash
			entry.Size = before.Size()
			entry.Modified = before.ModTime().UTC()
			stat := before.Sys().(*syscall.Stat_t)
			uid, gid, mode := stat.Uid, stat.Gid, uint32(before.Mode().Perm())
			entry.UID, entry.GID, entry.Mode = &uid, &gid, &mode
		}
		b, e := json.MarshalIndent(entry, "", "  ")
		if e != nil {
			return entries, e
		}
		if e = backupWrite(dest, name+".json", append(b, '\n')); e != nil {
			return entries, e
		}
		entries = append(entries, entry)
	}
	// Originals, metadata and containing directory entries must be durable before
	// the first instruction or installation-manifest mutation is attempted.
	for _, root := range []*os.Root{dest, parent, r} {
		if e = syncRoot(root); e != nil {
			return entries, e
		}
	}
	if e = s.check(home, true); e != nil {
		return entries, e
	}
	return entries, nil
}
func (s *snapshot) acceptChange(path string, c *FileChange) error {
	if s == nil {
		return nil
	}
	want, ok := s.changes[path]
	if !ok || c == nil || want.BeforeExists != c.BeforeExists || want.AfterExists != c.AfterExists || want.BeforeHash != c.BeforeHash || want.AfterHash != c.AfterHash {
		return model.Err("plan_changed", "computed instruction changes differ from the reviewed preview")
	}
	return nil
}
