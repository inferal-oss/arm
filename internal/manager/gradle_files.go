package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/storage"
	"golang.org/x/sys/unix"
)

type gradleDirIdentity = model.FileIdentity

func gradleDirStat(fd int) (gradleDirIdentity, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return gradleDirIdentity{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0077 != 0 || st.Uid != uint32(os.Geteuid()) {
		return gradleDirIdentity{}, model.Err("unsafe_path", "Gradle private directory must be owned, private and regular")
	}
	return storage.DirectoryIdentity(fd)
}

// Every operation starts from the checked managed-storage allocation, then uses
// descriptor-relative no-follow paths and the recorded private child identity.
func (m *Manager) openGradleDirectory(storageID, name string, expected *gradleDirIdentity, create bool) (*os.File, gradleDirIdentity, error) {
	var none gradleDirIdentity
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return nil, none, model.Err("unsafe_path", "invalid Gradle private directory name")
	}
	store, err := m.get(storageID)
	if err != nil {
		return nil, none, err
	}
	if m.storage == nil || store.Artifact == nil || store.Artifact.Identity == nil || store.Artifact.Phase != "owned" {
		return nil, none, model.Err("storage_unavailable", "Gradle workspace needs verified owned managed storage")
	}
	if err := m.storage.Check(*store.Artifact); err != nil {
		return nil, none, err
	}
	parent, err := unix.Open(store.ManagedDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, none, err
	}
	defer unix.Close(parent)
	var st unix.Stat_t
	if err := unix.Fstat(parent, &st); err != nil {
		return nil, none, err
	}
	id := store.Artifact.Identity
	opened, err := storage.DirectoryIdentity(parent)
	if err != nil {
		return nil, none, err
	}
	if opened != *id || st.Mode&0077 != 0 {
		return nil, none, model.Err("unsafe_path", "managed Gradle workspace parent identity changed")
	}
	if create {
		if expected != nil {
			return nil, none, fmt.Errorf("cannot create an already identified Gradle workspace")
		}
		if err := unix.Mkdirat(parent, name, 0700); err != nil {
			return nil, none, err
		}
		if err := unix.Fsync(parent); err != nil {
			return nil, none, err
		}
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, none, err
	}
	directory := os.NewFile(uintptr(fd), name)
	actual, err := gradleDirStat(fd)
	if err == nil && expected != nil && actual != *expected {
		err = model.Err("unsafe_path", "Gradle private directory identity changed")
	}
	if err == nil {
		err = m.storage.Check(*store.Artifact)
	}
	if err != nil {
		directory.Close()
		return nil, none, err
	}
	return directory, actual, nil
}

func gradleWriteFile(dir *os.File, name string, data []byte) error {
	if filepath.Base(name) != name || name == "." || name == ".." || len(data) > 64<<10 {
		return model.Err("unsafe_path", "invalid bounded Gradle control file")
	}
	if _, err := gradleDirStat(int(dir.Fd())); err != nil {
		return err
	}
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = dir.Sync()
	}
	return err
}

func gradleReadJSON(dir *os.File, name string, result any) error {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return model.Err("unsafe_path", "invalid Gradle evidence name")
	}
	if _, err := gradleDirStat(int(dir.Fd())); err != nil {
		return err
	}
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var before unix.Stat_t
	if err = unix.Fstat(fd, &before); err != nil {
		return err
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0077 != 0 || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 || before.Size < 1 || before.Size > 64<<10 {
		return model.Err("unsafe_path", "Gradle evidence must be a bounded private owned single-link regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return model.Err("unsafe_path", "Gradle evidence exceeds its bounded read")
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return err
	}
	before.Atim = after.Atim // The evidence read may update access time.
	if before != after {
		return model.Err("unsafe_path", "Gradle evidence changed during observation")
	}
	var current unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	current.Atim = after.Atim
	if current != after {
		return model.Err("unsafe_path", "Gradle evidence path changed during observation")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return model.Err("invalid_evidence", "Gradle evidence contains trailing data")
	}
	return nil
}
