package meshsshrelay

import (
	"errors"
	"sync"
	"time"
)

// PacerPool shares one rate budget across all simultaneous routed streams and
// both directions under the same exact grant, including SSH and TCP handlers.
// Expired grants are retired; too many unexpired revisions deny new streams.
type PacerPool struct {
	mu     sync.Mutex
	grants map[RelayGrant]*Pacer
}

func (p *PacerPool) ForGrant(g RelayGrant) (*Pacer, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if !now.Before(g.Expires) {
		return nil, errors.New("transit rate grant expired")
	}
	for key := range p.grants {
		if !now.Before(key.Expires) {
			delete(p.grants, key)
		}
	}
	if limiter := p.grants[g]; limiter != nil {
		return limiter, nil
	}
	if len(p.grants) >= 256 {
		return nil, errors.New("bounded transit rate revisions exhausted")
	}
	limiter, err := NewPacer(g.BytesPerSecond)
	if err != nil {
		return nil, err
	}
	if p.grants == nil {
		p.grants = make(map[RelayGrant]*Pacer)
	}
	p.grants[g] = limiter
	return limiter, nil
}
