// Package sshservice implements SSH protocol boundaries without granting a
// process launch. Destination supervision is supplied by a separate adapter.
package sshservice

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var errSigningDenied = errors.New("ephemeral SSH signing request denied")

// UserSigner is deliberately not a general agent/keyring. It signs only a
// bounded SSH publickey userauth packet for this invocation's account and key.
// The session hash is verified by the destination SSH handshake; standard
// publickey userauth does not contain a host name. Same-UID processes are not
// a security isolation boundary for this private per-invocation service.
type UserSigner struct {
	mu        sync.Mutex
	key       ssh.Signer
	public    []byte
	account   string
	deadline  time.Time
	remaining int
}

func NewUserSigner(account string, lifetime time.Duration) (*UserSigner, error) {
	if !validAccount(account) || lifetime <= 0 || lifetime > 30*time.Second {
		return nil, errSigningDenied
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	key, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, err
	}
	return &UserSigner{key: key, public: key.PublicKey().Marshal(), account: account, deadline: time.Now().Add(lifetime), remaining: 4}, nil
}
func (s *UserSigner) PublicKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, _ := ssh.ParsePublicKey(s.public)
	return key
}
func (s *UserSigner) Close() { s.mu.Lock(); defer s.mu.Unlock(); s.key = nil; s.remaining = 0 }
func (s *UserSigner) List() ([]*agent.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil || !time.Now().Before(s.deadline) {
		return nil, errSigningDenied
	}
	return []*agent.Key{{Format: ssh.KeyAlgoED25519, Blob: append([]byte(nil), s.public...), Comment: "ARM ephemeral invocation"}}, nil
}
func (s *UserSigner) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return s.SignWithFlags(key, data, 0)
}
func (s *UserSigner) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil || !time.Now().Before(s.deadline) || s.remaining <= 0 || flags != 0 || key == nil || !bytes.Equal(key.Marshal(), s.public) || !validUserAuth(data, s.account, s.public) {
		return nil, errSigningDenied
	}
	s.remaining--
	return s.key.Sign(rand.Reader, data)
}
func (s *UserSigner) Add(agent.AddedKey) error       { return errSigningDenied }
func (s *UserSigner) Remove(ssh.PublicKey) error     { return errSigningDenied }
func (s *UserSigner) RemoveAll() error               { return errSigningDenied }
func (s *UserSigner) Lock([]byte) error              { return errSigningDenied }
func (s *UserSigner) Unlock([]byte) error            { return errSigningDenied }
func (s *UserSigner) Signers() ([]ssh.Signer, error) { return nil, errSigningDenied }
func (s *UserSigner) Extension(string, []byte) ([]byte, error) {
	return nil, agent.ErrExtensionUnsupported
}
func validUserAuth(raw []byte, account string, key []byte) bool {
	if len(raw) > 8192 {
		return false
	}
	p := wireParser{raw: raw}
	session, ok := p.string(64)
	if !ok || (len(session) != 32 && len(session) != 48 && len(session) != 64) || !p.byte(50) {
		return false
	}
	user, ok := p.string(256)
	if !ok || string(user) != account {
		return false
	}
	service, ok := p.string(64)
	if !ok || string(service) != "ssh-connection" {
		return false
	}
	method, ok := p.string(64)
	if !ok || string(method) != "publickey" || !p.byte(1) {
		return false
	}
	algorithm, ok := p.string(64)
	if !ok || string(algorithm) != ssh.KeyAlgoED25519 {
		return false
	}
	blob, ok := p.string(1024)
	return ok && bytes.Equal(blob, key) && len(p.raw) == 0
}

type wireParser struct{ raw []byte }

func (p *wireParser) string(max int) ([]byte, bool) {
	if len(p.raw) < 4 {
		return nil, false
	}
	n := binary.BigEndian.Uint32(p.raw)
	p.raw = p.raw[4:]
	if n > uint32(max) || int(n) > len(p.raw) {
		return nil, false
	}
	v := p.raw[:n]
	p.raw = p.raw[n:]
	return v, true
}
func (p *wireParser) byte(want byte) bool {
	if len(p.raw) == 0 || p.raw[0] != want {
		return false
	}
	p.raw = p.raw[1:]
	return true
}

// ServeAgentConnection has no listener/path side effects. Callers supply a
// verified private socket and close it after the SSH handshake. Guard frames
// BEFORE the upstream agent allocates their advertised payload.
func ServeAgentConnection(ctx context.Context, c net.Conn, s *UserSigner) error {
	if s == nil {
		return errSigningDenied
	}
	s.mu.Lock()
	deadline := s.deadline
	s.mu.Unlock()
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	defer c.Close()
	return agent.ServeAgent(s, &agentFrames{ReadWriter: c})
}

type agentFrames struct {
	io.ReadWriter
	header    []byte
	remaining uint32
	requests  int
}

func (f *agentFrames) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(f.header) == 0 && f.remaining == 0 {
		f.requests++
		if f.requests > 32 {
			return 0, fmt.Errorf("ephemeral agent request count exceeded")
		}
		var h [4]byte
		if _, err := io.ReadFull(f.ReadWriter, h[:]); err != nil {
			return 0, err
		}
		n := binary.BigEndian.Uint32(h[:])
		if n == 0 || n > 8192 {
			return 0, fmt.Errorf("ephemeral agent frame exceeds bound")
		}
		f.header = append([]byte(nil), h[:]...)
		f.remaining = n
	}
	if len(f.header) > 0 {
		n := copy(p, f.header)
		f.header = f.header[n:]
		return n, nil
	}
	n, err := f.ReadWriter.Read(p[:min(len(p), int(f.remaining))])
	f.remaining -= uint32(n)
	return n, err
}
