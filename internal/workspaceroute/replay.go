package workspaceroute

import (
	"sync"
	"time"
)

// admissionBudget bounds open rate and remembers recent route IDs after close.
// IDs are not authentication; every stream is separately currently authorized.
// A flood refuses rather than evicting an unexpired replay entry.
type admissionBudget struct {
	mu     sync.Mutex
	routes map[string]time.Time
	peers  map[string]routeBucket
	global map[string]routeBucket
}
type routeBucket struct {
	at     time.Time
	tokens float64
}

func take(b routeBucket, now time.Time, burst, rate float64) (routeBucket, bool) {
	if b.at.IsZero() {
		b = routeBucket{now, burst}
	}
	if now.Before(b.at) {
		return b, false
	}
	b.tokens = min(burst, b.tokens+now.Sub(b.at).Seconds()*rate)
	b.at = now
	if b.tokens < 1 {
		return b, false
	}
	b.tokens--
	return b, true
}
func (b *admissionBudget) admit(peer, key string, until time.Time, class string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.routes == nil {
		b.routes = map[string]time.Time{}
		b.peers = map[string]routeBucket{}
		b.global = map[string]routeBucket{}
	}
	for id, expiry := range b.routes {
		if !now.Before(expiry) {
			delete(b.routes, id)
		}
	}
	for id, bucket := range b.peers {
		if now.Sub(bucket.at) > time.Minute {
			delete(b.peers, id)
		}
	}
	if _, exists := b.routes[key]; exists || len(b.routes) >= 4096 || len(b.peers) >= 192 && b.peers[peer+":"+class].at.IsZero() {
		return false
	}
	globalBurst, globalRate, peerBurst, peerRate := 192.0, 96.0, 48.0, 24.0
	if class != ClassMessage {
		globalBurst, globalRate, peerBurst, peerRate = 32, 16, 8, 4
	}
	global, gok := take(b.global[class], now, globalBurst, globalRate)
	local, lok := take(b.peers[peer+":"+class], now, peerBurst, peerRate)
	if !gok || !lok {
		return false
	}
	b.global[class] = global
	b.peers[peer+":"+class] = local
	b.routes[key] = minTime(until, now.Add(MaxLifetime))
	return true
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
