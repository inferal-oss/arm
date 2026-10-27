package mesh

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// LocalForwardProtocol separates the token-gated loopback service from mesh
// RPC and the end-to-end routed forward protocol.
const LocalForwardProtocol = "arm.local.forward.v1"

// LocalForwardServerTLS uses the current native mesh signing identity already
// held by Store. A listener using this snapshot must close when its owning
// lease ends. A later identity renewal makes clients reject this snapshot
// until restarted.
func (s *Store) LocalForwardServerTLS() (*tls.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("mesh store closed")
	}
	identity := s.config.Identity
	certificate, err := ValidateIdentity(identity)
	if err != nil {
		return nil, err
	}
	if len(s.cert.Certificate) != 1 || !bytes.Equal(s.cert.Certificate[0], certificate.Raw) {
		return nil, errors.New("local forward signing certificate differs from current mesh identity")
	}
	return &tls.Config{
		Certificates:           []tls.Certificate{s.cert},
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		NextProtos:             []string{LocalForwardProtocol},
		SessionTicketsDisabled: true,
	}, nil
}

// LocalForwardClientTLS trusts only the existing, validated local mesh
// identity. It has no alternate root, hostname override or plaintext fallback.
func LocalForwardClientTLS(identity Identity) (*tls.Config, error) {
	certificate, err := ValidateIdentity(identity)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		RootCAs:                roots,
		ServerName:             SAN(identity.Host),
		NextProtos:             []string{LocalForwardProtocol},
		SessionTicketsDisabled: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.Version != tls.VersionTLS13 || state.DidResume || state.NegotiatedProtocol != LocalForwardProtocol || len(state.PeerCertificates) != 1 || len(state.VerifiedChains) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, certificate.Raw) {
				return fmt.Errorf("local TCP source certificate or protocol differs from pinned mesh identity")
			}
			return nil
		},
	}, nil
}
