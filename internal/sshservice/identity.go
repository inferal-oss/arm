package sshservice

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const hostKeyFile = "ssh-host-key.json"

type hostRecord struct {
	Schema  int    `json:"schema"`
	Host    string `json:"host"`
	Private []byte `json:"private_pkcs8"`
}

// LoadHostKey is strictly read-only. Inventory must never call InitializeHostKey.
// dir is an existing canonical private service directory, not arbitrary HOME.
func LoadHostKey(dir, host string) (ssh.Signer, error) {
	d, err := privateDirectory(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	raw, err := readPrivate(d, hostKeyFile)
	if err != nil {
		return nil, err
	}
	if !validID(host) {
		return nil, fmt.Errorf("invalid host authority")
	}
	var v hostRecord
	if err = closedRecord(raw, &v, []string{"schema", "host", "private_pkcs8"}); err != nil || v.Schema != 1 || v.Host != host {
		return nil, fmt.Errorf("invalid SSH host identity record")
	}
	key, err := x509.ParsePKCS8PrivateKey(v.Private)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH private key")
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("SSH host key must be Ed25519")
	}
	return ssh.NewSignerFromKey(ed)
}

// InitializeHostKey is an explicit setup operation; it never replaces an
// established key, repairs a partial file, or creates a service directory.
func InitializeHostKey(dir, host string) (ssh.Signer, error) {
	if !validID(host) {
		return nil, fmt.Errorf("invalid host authority")
	}
	d, err := privateDirectory(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(hostRecord{1, host, private})
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(d.Fd()), hostKeyFile, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err == unix.EEXIST {
		return LoadHostKey(dir, host)
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), hostKeyFile)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = validateDirectory(d, dir); err != nil {
		return nil, err
	}
	if err = d.Sync(); err != nil {
		return nil, err
	}
	return LoadHostKey(dir, host)
}
func privateDirectory(path string) (*os.File, error) {
	if !safePath(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("canonical private service directory required")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range bytes.Split([]byte(path), []byte{'/'}) {
		if len(part) == 0 {
			continue
		}
		next, e := unix.Openat(fd, string(part), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		f.Close()
		return nil, fmt.Errorf("SSH service directory must be private and same-user")
	}
	return f, nil
}
func validateDirectory(d *os.File, path string) error {
	current, err := privateDirectory(path)
	if err != nil {
		return err
	}
	defer current.Close()
	var a, b unix.Stat_t
	if unix.Fstat(int(d.Fd()), &a) != nil || unix.Fstat(int(current.Fd()), &b) != nil || a.Dev != b.Dev || a.Ino != b.Ino {
		return fmt.Errorf("SSH service directory changed")
	}
	return nil
}
func readPrivate(d *os.File, name string) ([]byte, error) {
	fd, err := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Nlink != 1 || st.Size > 16384 {
		return nil, fmt.Errorf("SSH identity must be bounded private single-link same-user file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(raw) > 16384 || int64(len(raw)) != st.Size {
		return nil, fmt.Errorf("SSH identity changed while reading")
	}
	return raw, nil
}
func closedRecord(raw []byte, value any, names []string) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return fmt.Errorf("closed identity object required")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return err
		}
		name, ok := t.(string)
		if !ok || !allowed[name] || seen[name] {
			return fmt.Errorf("unknown or duplicate identity field")
		}
		seen[name] = true
		var part json.RawMessage
		if err = d.Decode(&part); err != nil || bytes.Equal(bytes.TrimSpace(part), []byte("null")) {
			return fmt.Errorf("invalid identity field")
		}
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF || len(seen) != len(names) {
		return fmt.Errorf("incomplete/trailing identity")
	}
	return json.Unmarshal(raw, value)
}
func publicDigest(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return hex.EncodeToString(sum[:])
}
