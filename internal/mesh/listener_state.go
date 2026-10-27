package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"getarm.org/internal/local"
	"time"
)

func (s *Store) SetListenerEnabled(enabled bool, expectedFingerprint, expectedListen string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.Identity.Fingerprint != expectedFingerprint || s.config.Listen != expectedListen {
		return fmt.Errorf("node identity/listener changed since review")
	}
	return s.update(func(config *Config) error { value := enabled; config.Enabled = &value; return nil })
}

func (s *Store) PrepareEndpoint(listen string) error {
	if err := validateListen(listen); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.ListenerEnabled() {
		return fmt.Errorf("existing enabled mesh endpoint is preserved; implicit rebind refused")
	}
	for _, peer := range s.config.Peers {
		if peer.State == "active" || peer.State == "pending" {
			return fmt.Errorf("cannot rebind a listener with existing desired peers")
		}
	}
	return s.update(func(config *Config) error { config.Listen = listen; config.Identity.Address = listen; return nil })
}

// DisableUnsharedListener preserves every other peer. Revoked tombstones are
// retained for audit; no lease/resource state is touched by this decision.
func (s *Store) DisableUnsharedListener() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Managed {
		return false, nil
	}
	for _, peer := range s.config.Peers {
		if peer.State == "active" || peer.State == "pending" {
			return false, nil
		}
	}
	err := s.update(func(config *Config) error { disabled := false; config.Enabled = &disabled; return nil })
	return err == nil, err
}

// RevokeExact is an administrative compare-and-revoke, not permission for a
// peer to change another node's trust. A delayed disconnect never affects a
// later enrollment under the same stable host ID.
type DisconnectReceipt struct {
	Key, Host, Pair, Fingerprint, Reason string
	Revision                             int
	At                                   time.Time
}

func (s *Store) RevokeExact(key, host, pair, fingerprint string, revision int, reason string) error {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	if !ValidKey(key) || reason == "" || len(reason) > 4096 {
		return fmt.Errorf("disconnect reason required and bounded")
	}
	s.mu.RLock()
	for _, receipt := range s.config.Disconnects {
		if receipt.Key == key {
			s.mu.RUnlock()
			if receipt.Host != host || receipt.Pair != pair || receipt.Fingerprint != fingerprint || receipt.Revision != revision || receipt.Reason != reason {
				return fmt.Errorf("disconnect key conflicts with recorded request")
			}
			return nil
		}
	}
	s.mu.RUnlock()
	peer, err := s.Resolve(host)
	if err != nil {
		return err
	}
	if peer.Pair != pair || peer.Identity.Fingerprint != fingerprint || peer.Revision != revision {
		return fmt.Errorf("peer enrollment changed; old disconnect must not revoke current trust")
	}
	// Linearize the comparison with the same store update as revocation. The
	// public Revoke lookup alone cannot preserve this CAS across re-pair.
	s.revoking.Store(peer.Identity.Host, true)
	s.mu.RLock()
	changed := s.changed
	s.mu.RUnlock()
	if changed != nil {
		changed(peer.Identity.Host)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(func(config *Config) error {
		if len(config.Disconnects) >= 256 {
			return fmt.Errorf("disconnect receipt history full; no audit evidence removed")
		}
		for index, current := range config.Peers {
			if current.Identity.Host != host {
				continue
			}
			if current.Pair != pair || current.Identity.Fingerprint != fingerprint || current.Revision != revision {
				return fmt.Errorf("peer enrollment changed before disconnect committed")
			}
			if current.State == "revoked" {
				return nil
			}
			config.Peers[index].State = "revoked"
			config.Peers[index].Revision++
			config.Peers[index].Reason = reason
			config.Disconnects = append(config.Disconnects, DisconnectReceipt{Key: key, Host: host, Pair: pair, Fingerprint: fingerprint, Revision: revision, Reason: reason, At: time.Now().UTC()})
			return audit(config, "revoked", config.Peers[index], "local:"+local.CurrentIdentity().UID)
		}
		return fmt.Errorf("peer no longer present")
	})
}

type ListenerActivation struct {
	Key         string `json:"key"`
	TokenSHA256 string `json:"token_sha256"`
}

func activationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (c Config) AppliedListener(key, token string) bool {
	return c.Activation != nil && c.Activation.Key == key && c.Activation.TokenSHA256 == activationToken(token)
}
func (s *Store) ApproveListener(key, token, pin, listen string) error {
	if !ValidKey(key) || token == "" {
		return fmt.Errorf("exact listener setup key/token required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.Identity.Fingerprint != pin || s.config.Listen != listen {
		return fmt.Errorf("listener identity/endpoint changed since review")
	}
	return s.update(func(config *Config) error {
		enabled := true
		config.Enabled = &enabled
		config.Activation = &ListenerActivation{Key: key, TokenSHA256: activationToken(token)}
		return nil
	})
}
