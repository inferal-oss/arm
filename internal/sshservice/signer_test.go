package sshservice

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func userAuth(key ssh.PublicKey, user string) []byte {
	return ssh.Marshal(struct {
		Session               []byte
		Message               byte
		User, Service, Method string
		HasKey                bool
		Algorithm             string
		Public                []byte
	}{make([]byte, 32), 50, user, "ssh-connection", "publickey", true, ssh.KeyAlgoED25519, key.Marshal()})
}
func TestEphemeralSignerAcceptsOnlyExactUserAuthAndBoundedAttempts(t *testing.T) {
	s, err := NewUserSigner("armuser", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := s.PublicKey()
	data := userAuth(key, "armuser")
	for _, bad := range [][]byte{[]byte("arbitrary signing"), userAuth(key, "other"), append(append([]byte{}, data...), 0), data[:len(data)-1], make([]byte, 8193)} {
		if _, err = s.Sign(key, bad); err == nil {
			t.Fatal("arbitrary/changed packet signed")
		}
	}
	if _, err = s.SignWithFlags(key, data, agent.SignatureFlagRsaSha256); err == nil {
		t.Fatal("unsupported signing flags accepted")
	}
	for i := 0; i < 4; i++ {
		signature, err := s.Sign(key, data)
		if err != nil || key.Verify(data, signature) != nil {
			t.Fatal("valid SSH proof failed", err)
		}
	}
	if _, err = s.Sign(key, data); err == nil {
		t.Fatal("signing attempt bound ignored")
	}
	if s.RemoveAll() == nil || s.Lock(nil) == nil || s.Unlock(nil) == nil || s.Add(agent.AddedKey{}) == nil || s.Remove(key) == nil {
		t.Fatal("general keyring operation accepted")
	}
	if _, err = s.Signers(); err == nil {
		t.Fatal("private signer exposed")
	}
	s.Close()
	if _, err = s.List(); err == nil {
		t.Fatal("closed identity exposed")
	}
}
func TestAgentWireProtocolAndPreallocationBound(t *testing.T) {
	for _, kind := range []string{"proof", "oversize", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := NewUserSigner("armuser", time.Second)
			defer s.Close()
			client, server := net.Pipe()
			defer client.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- ServeAgentConnection(ctx, server, s) }()
			switch kind {
			case "proof":
				a := agent.NewClient(client)
				keys, err := a.List()
				if err != nil || len(keys) != 1 {
					t.Fatal(keys, err)
				}
				data := userAuth(s.PublicKey(), "armuser")
				sig, err := a.Sign(s.PublicKey(), data)
				if err != nil || s.PublicKey().Verify(data, sig) != nil {
					t.Fatal("agent authentication proof failed", err)
				}
				client.Close()
			case "oversize":
				var h [4]byte
				binary.BigEndian.PutUint32(h[:], 8193)
				if _, err := client.Write(h[:]); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("agent termination had no reason")
				}
			case <-time.After(time.Second):
				t.Fatal("agent connection did not terminate")
			}
		})
	}
}
func TestSignerDeadlineCannotBecomeReusableAgent(t *testing.T) {
	s, _ := NewUserSigner("armuser", time.Millisecond)
	defer s.Close()
	time.Sleep(3 * time.Millisecond)
	if _, err := s.Sign(s.PublicKey(), userAuth(s.PublicKey(), "armuser")); err == nil {
		t.Fatal("expired signer remained usable")
	}
}
