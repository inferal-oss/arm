package sshservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

type Pin struct {
	Schema                int       `json:"schema"`
	Source                string    `json:"source"`
	Target                string    `json:"target"`
	Pair                  string    `json:"pair"`
	Revision              int       `json:"revision"`
	TLSFingerprint        string    `json:"tls_fingerprint"`
	TLSSPKI               string    `json:"tls_spki"`
	Account               string    `json:"account"`
	PublicKey             string    `json:"public_key"`
	At                    time.Time `json:"at"`
	RouteWorkspace        string    `json:"route_workspace,omitempty"`
	RouteGeneration       string    `json:"route_generation,omitempty"`
	RouteDirectoryPin     string    `json:"route_directory_pin,omitempty"`
	RouteDirectorySPKI    string    `json:"route_directory_spki,omitempty"`
	RouteSourceGeneration int64     `json:"route_source_generation,omitempty"`
	RouteTargetGeneration int64     `json:"route_target_generation,omitempty"`
}
type pinSnapshot struct {
	Present       bool
	Device, Inode uint64
	SHA           string
}

// PinReview is a concrete immutable review. commitPin does no TOFU: the caller
// must explicitly accept this snapshot; it may not reconstruct a replacement.
type PinReview struct {
	Before                          *Pin
	After                           Pin
	expected                        pinSnapshot
	digest                          string
	directory                       string
	directoryDevice, directoryInode uint64
}

func (p Pin) Validate() error {
	if p.Schema != 1 || !validID(p.Source) || !validID(p.Target) || p.Source == p.Target || !hexString(p.Pair, 64) || p.Revision < 1 || !hexString(p.TLSFingerprint, 64) || !hexString(p.TLSSPKI, 64) || !validAccount(p.Account) || p.At.IsZero() {
		return fmt.Errorf("invalid enrollment-bound SSH pin")
	}
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(p.PublicKey))
	if err != nil || key.Type() != ssh.KeyAlgoED25519 || len(rest) != 0 || strings.TrimSpace(p.PublicKey) != strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) {
		return fmt.Errorf("canonical Ed25519 SSH pin required")
	}
	if p.RouteWorkspace != "" || p.RouteGeneration != "" || p.RouteDirectoryPin != "" || p.RouteDirectorySPKI != "" ||
		p.RouteSourceGeneration != 0 || p.RouteTargetGeneration != 0 {
		if !hexString(p.RouteWorkspace, 32) || !hexString(p.RouteGeneration, 32) || !hexString(p.RouteDirectoryPin, 64) ||
			!hexString(p.RouteDirectorySPKI, 64) || p.RouteSourceGeneration < 1 || p.RouteTargetGeneration < 1 {
			return fmt.Errorf("invalid exact routed SSH workspace binding")
		}
	}
	return nil
}
func ReviewPin(dir string, next Pin) (PinReview, error) {
	var review PinReview
	if err := next.Validate(); err != nil {
		return review, err
	}
	d, err := privateDirectory(dir)
	if err != nil {
		return review, err
	}
	defer d.Close()
	old, state, err := readPin(d, next.Target)
	if err != nil {
		return review, err
	}
	if old != nil && old.Source != next.Source {
		return review, fmt.Errorf("pin registry belongs to another local authority")
	}
	raw, _ := json.Marshal(next)
	sum := sha256.Sum256(raw)
	var st unix.Stat_t
	if err = unix.Fstat(int(d.Fd()), &st); err != nil {
		return review, err
	}
	return PinReview{old, next, state, hex.EncodeToString(sum[:]), dir, uint64(st.Dev), st.Ino}, nil
}

// ReadPin never creates a key, lock or service directory.
func ReadPin(dir, target string) (*Pin, error) {
	if !validID(target) {
		return nil, fmt.Errorf("invalid target")
	}
	d, err := privateDirectory(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	p, _, err := readPin(d, target)
	return p, err
}
func readPin(d *os.File, target string) (*Pin, pinSnapshot, error) {
	name := "ssh-pin-" + target + ".json"
	var st unix.Stat_t
	if err := unix.Fstatat(int(d.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err == unix.ENOENT {
		return nil, pinSnapshot{}, nil
	} else if err != nil {
		return nil, pinSnapshot{}, err
	}
	raw, err := readPrivate(d, name)
	if err != nil {
		return nil, pinSnapshot{}, err
	}
	var after unix.Stat_t
	if unix.Fstatat(int(d.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Dev != after.Dev || st.Ino != after.Ino || st.Size != after.Size {
		return nil, pinSnapshot{}, fmt.Errorf("SSH pin changed while reading")
	}
	var p Pin
	routedFields, err := closedRecordWithOptional(raw, &p,
		[]string{"schema", "source", "target", "pair", "revision", "tls_fingerprint", "tls_spki", "account", "public_key", "at"},
		[]string{"route_workspace", "route_generation", "route_directory_pin", "route_directory_spki", "route_source_generation", "route_target_generation"})
	if err != nil {
		return nil, pinSnapshot{}, err
	}
	if routedFields && p.RouteWorkspace == "" {
		return nil, pinSnapshot{}, fmt.Errorf("explicit empty routed SSH pin extension")
	}
	if err = p.Validate(); err != nil || p.Target != target {
		return nil, pinSnapshot{}, fmt.Errorf("SSH pin identity invalid")
	}
	sum := sha256.Sum256(raw)
	return &p, pinSnapshot{true, uint64(st.Dev), st.Ino, hex.EncodeToString(sum[:])}, nil
}
func commitPin(ctx context.Context, review PinReview, guards ...func() error) error {
	check := func() error {
		for _, guard := range guards {
			if guard != nil {
				if e := guard(); e != nil {
					return e
				}
			}
		}
		return ctx.Err()
	}
	if e := check(); e != nil {
		return e
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(review.After)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if review.digest == "" || hex.EncodeToString(sum[:]) != review.digest || review.After.Validate() != nil {
		return fmt.Errorf("reviewed SSH pin changed")
	}
	d, err := privateDirectory(review.directory)
	if err != nil {
		return err
	}
	defer d.Close()
	var directoryStat unix.Stat_t
	if unix.Fstat(int(d.Fd()), &directoryStat) != nil || uint64(directoryStat.Dev) != review.directoryDevice || directoryStat.Ino != review.directoryInode {
		return fmt.Errorf("reviewed SSH pin directory changed")
	}
	fd, err := unix.Openat(int(d.Fd()), "ssh-pins.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), "ssh-pins.lock")
	defer lock.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size != 0 {
		return fmt.Errorf("unsafe SSH pin lock")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	var named unix.Stat_t
	if unix.Fstatat(int(d.Fd()), "ssh-pins.lock", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || named.Dev != st.Dev || named.Ino != st.Ino || named.Mode != st.Mode || named.Uid != st.Uid || named.Nlink != 1 || named.Size != 0 {
		return fmt.Errorf("SSH pin lock replaced")
	}
	if err = validateDirectory(d, review.directory); err != nil {
		return err
	}
	old, current, err := readPin(d, review.After.Target)
	if err != nil || current != review.expected || !reflect.DeepEqual(old, review.Before) {
		return fmt.Errorf("reviewed SSH pin is stale")
	}
	// Bound the complete namespace scan; never silently omit pending files.
	duplicate, err := unix.Openat(int(d.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	scan := os.NewFile(uintptr(duplicate), review.directory)
	names, err := scan.Readdirnames(4097)
	scan.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) > 4096 {
		return fmt.Errorf("SSH service namespace exceeds bound")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".ssh-pin-write-" + hex.EncodeToString(random[:])
	fd, err = unix.Openat(int(d.Fd()), temp, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), temp)
	var created unix.Stat_t
	if err = unix.Fstat(fd, &created); err != nil {
		f.Close()
		return err
	}
	defer func() {
		var current unix.Stat_t
		if unix.Fstatat(int(d.Fd()), temp, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == created.Dev && current.Ino == created.Ino {
			_ = unix.Unlinkat(int(d.Fd()), temp, 0)
		}
	}()
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	_, current, err = readPin(d, review.After.Target)
	if err != nil || current != review.expected {
		return fmt.Errorf("SSH pin changed before publication")
	}
	if err = validateDirectory(d, review.directory); err != nil {
		return err
	}
	if unix.Fstatat(int(d.Fd()), "ssh-pins.lock", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || named.Dev != st.Dev || named.Ino != st.Ino || named.Mode != st.Mode || named.Uid != st.Uid || named.Nlink != 1 || named.Size != 0 {
		return fmt.Errorf("SSH pin lock changed before publication")
	}
	if err = check(); err != nil {
		return err
	}
	name := "ssh-pin-" + review.After.Target + ".json"
	if !review.expected.Present {
		if err = unix.Linkat(int(d.Fd()), temp, int(d.Fd()), name, 0); err == nil {
			err = unix.Unlinkat(int(d.Fd()), temp, 0)
		}
	} else {
		err = unix.Renameat(int(d.Fd()), temp, int(d.Fd()), name)
	}
	if err != nil {
		return err
	}
	return d.Sync()
}
func hexString(s string, n int) bool {
	if len(s) != n || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func pinPath(dir, target string) string { return filepath.Join(dir, "ssh-pin-"+target+".json") }
