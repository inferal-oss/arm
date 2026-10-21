package local

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

func Home() string {
	if p := os.Getenv("ARM_HOME"); p != "" {
		return p
	}
	p, e := os.UserConfigDir()
	if e != nil {
		panic(e)
	}
	return filepath.Join(p, "arm")
}

func Prepare(home string) error {
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	info, e := os.Lstat(home)
	if e != nil {
		return e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("ARM state directory must be a real directory, not a symlink")
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("ARM state directory must be private (mode 0700); existing directory permissions were not changed")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("ARM state directory belongs to another user")
	}
	return nil
}

func Lock(home, name string) (*os.File, error) {
	if e := Prepare(home); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(home, name), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another ARM integration operation is in progress: %w", e)
	}
	return f, nil
}

func AtomicJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".arm-write-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(append(b, '\n'))
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(name, path)
}

func ReadEndpoint(home string) (Endpoint, error) {
	var ep Endpoint
	b, e := os.ReadFile(filepath.Join(home, "endpoint.json"))
	if e != nil {
		return ep, e
	}
	e = json.Unmarshal(b, &ep)
	return ep, e
}
