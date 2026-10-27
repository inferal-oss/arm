//go:build arm_devtrace

package mesh

import (
	"errors"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

// Caller retains the actual Store.mu RLock used by WithPeerSnapshot. No second
// Config observation is substituted for the refusing authorization snapshot.
func traceNodeStopGrantRefused(s *Store, host string, err error) {
	if !nodestop.DevEnabled(s.home) {
		return
	}
	var peer *Peer
	for _, p := range s.config.Peers {
		if p.Identity.Host == host {
			v := clonePeer(p)
			peer = &v
			break
		}
	}
	var api *model.Error
	code := ""
	if errors.As(err, &api) {
		code = api.Code
	}
	nodestop.DevEmit(s.home, "grant.guard-refused", struct {
		Source string `json:"source"`
		Peer   *Peer  `json:"peer"`
		Code   string `json:"code"`
		API    string `json:"api"`
	}{host, peer, code, "Store.WithPeerSnapshot"})
}
