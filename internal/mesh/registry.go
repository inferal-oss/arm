package mesh

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ReadRegistry reads existing public identity/trust configuration using private
// no-follow file checks. It never prepares directories, reads private keys,
// initializes identity, probes interfaces or consults the SSH environment.
// Expired identities remain inspectable and cannot authenticate new requests.
func ReadRegistry(home string) (Config, bool, error) {
	var config Config
	raw, err := privateFile(filepath.Join(home, "mesh", "config.json"))
	if os.IsNotExist(err) {
		return config, false, nil
	}
	if err != nil {
		return config, false, err
	}
	if err = Decode(raw, &config); err != nil {
		return config, false, err
	}
	if err = ValidateRegistry(config); err != nil {
		return Config{}, false, err
	}
	return config, true, nil
}

func validRegistryOwner(identity Identity) bool {
	uid, err := strconv.ParseUint(identity.Owner, 10, 32)
	return err == nil && strconv.FormatUint(uid, 10) == identity.Owner && utf8.ValidString(identity.OwnerUsername) && len(identity.OwnerUsername) <= 256 && !strings.ContainsFunc(identity.OwnerUsername, unicode.IsControl)
}

// ValidateRegistry validates a local operator registry, including the recorded
// local owner against the authenticated kernel account. Peer owner names remain
// reviewed display snapshots; they do not authenticate processes on this host.
func ValidateRegistry(config Config) error {
	if config.Identity.Owner != strconv.Itoa(os.Geteuid()) || !validRegistryOwner(config.Identity) {
		return fmt.Errorf("local registry owner differs from authenticated account")
	}

	if _, err := parseIdentity(config.Identity, false); err != nil {
		return err
	}
	if err := validateListen(config.Listen); err != nil {
		return err
	}
	if len(config.Peers) > 64 {
		return fmt.Errorf("registry exceeds64 direct peers")
	}
	seen := map[string]bool{}
	for _, peer := range config.Peers {
		if peer.Identity.Host == config.Identity.Host || seen[peer.Identity.Host] || peer.Revision < 1 || (peer.State != "active" && peer.State != "pending" && peer.State != "revoked") {
			return fmt.Errorf("registry has invalid peer identity/state/revision")
		}
		pair, err := hex.DecodeString(peer.Pair)
		if err != nil || len(pair) != 32 || (peer.State == "active" && !peer.Proven) {
			return fmt.Errorf("registry has invalid enrollment/proof")
		}
		if !validRegistryOwner(peer.Identity) {
			return fmt.Errorf("paired owner snapshot has invalid shape")
		}
		seen[peer.Identity.Host] = true
		if _, err := parseIdentity(peer.Identity, false); err != nil {
			return err
		}
		if err := peer.Grant.Validate(); err != nil {
			return err
		}
	}
	return nil
}
