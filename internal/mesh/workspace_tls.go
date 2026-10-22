package mesh

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/workspace"
)

const WorkspaceInnerProtocol = "arm.workspace.directory.v1"

func publicWorkspaceIdentity(i Identity) workspace.Identity {
	return workspace.Identity{Host: i.Host, Name: i.Name, Certificate: i.Certificate, Fingerprint: i.Fingerprint, SPKI: i.SPKI, Expires: i.Expires}
}

// WorkspaceIdentityCurrent checks only the retained local signing identity.
// It is not membership, a direct-peer grant or a substitute for the central
// ledger's current workspace authorization at every final operation.
func (s *Store) WorkspaceIdentityCurrent(expected workspace.Identity) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || publicWorkspaceIdentity(s.config.Identity) != expected {
		return fmt.Errorf("current native workspace signing identity changed or closed")
	}
	if _, e := ValidateIdentity(s.config.Identity); e != nil {
		return e
	}
	if len(s.cert.Certificate) != 1 {
		return fmt.Errorf("workspace signing certificate missing")
	}
	cert, e := workspace.ValidateIdentity(expected)
	if e != nil {
		return e
	}
	if !bytes.Equal(cert.Raw, s.cert.Certificate[0]) {
		return fmt.Errorf("retained workspace signing key/certificate differs")
	}
	return nil
}

// WorkspaceTLS builds a single-purpose inner config for exactly one recorded
// local and remote workspace identity. It NEVER alters outer ServerTLS pools,
// pairs a member, exports the private key or confers any operator/RPC grant.
// Callers must obtain expectedRemote from their current class-specific ledger
// gate and revalidate that gate after handshake, before handling an operation,
// and during the bounded stream. Remote identity JSON is not authorization.
func (s *Store) WorkspaceTLS(expectedLocal, expectedRemote workspace.Identity, server bool) (*tls.Config, error) {
	if expectedLocal.Host == expectedRemote.Host {
		return nil, fmt.Errorf("workspace inner endpoints must be distinct")
	}
	if e := s.WorkspaceIdentityCurrent(expectedLocal); e != nil {
		return nil, e
	}
	remote, e := workspace.ValidateIdentity(expectedRemote)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	if now.Before(remote.NotBefore) || !now.Before(remote.NotAfter) {
		return nil, fmt.Errorf("workspace peer certificate is not currently valid")
	}
	s.mu.RLock()
	if s.closed || publicWorkspaceIdentity(s.config.Identity) != expectedLocal {
		s.mu.RUnlock()
		return nil, fmt.Errorf("workspace native identity changed while preparing TLS")
	}
	localCert := s.cert
	s.mu.RUnlock()
	roots := x509.NewCertPool()
	roots.AddCert(remote)
	config := &tls.Config{Certificates: []tls.Certificate{localCert}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{WorkspaceInnerProtocol}, SessionTicketsDisabled: true}
	if server {
		config.ClientAuth = tls.RequireAndVerifyClientCert
		config.ClientCAs = roots
	} else {
		config.RootCAs = roots
		config.ServerName = SAN(expectedRemote.Host)
	}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if state.Version != tls.VersionTLS13 || state.DidResume || state.NegotiatedProtocol != WorkspaceInnerProtocol || len(state.PeerCertificates) != 1 || len(state.VerifiedChains) == 0 {
			return fmt.Errorf("workspace TLS protocol/normal verification differs")
		}
		certificate := state.PeerCertificates[0]
		der := sha256.Sum256(certificate.Raw)
		spki := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
		if hex.EncodeToString(der[:]) != expectedRemote.Fingerprint || hex.EncodeToString(spki[:]) != expectedRemote.SPKI || !bytes.Equal(certificate.Raw, remote.Raw) || len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != SAN(expectedRemote.Host) {
			return fmt.Errorf("workspace exact peer certificate/SPKI/SAN differs")
		}
		return s.WorkspaceIdentityCurrent(expectedLocal)
	}
	return config, nil
}
