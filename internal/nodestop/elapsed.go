package nodestop

import (
	"fmt"
	"sync"
	"time"
)

// ElapsedReview is an in-process lifecycle-review lifetime, never wire authority.
// Its clock includes suspend. Restart/reboot loses the object rather than
// reviving serialized timestamps; unavailable/regressing clocks fail closed.
type ElapsedReview struct {
	mu          sync.Mutex
	now         func() (int64, error)
	first, last int64
	failure     error
}

func NewElapsedReview() (*ElapsedReview, error) { return newElapsedReview(nativeReviewTick) }
func newElapsedReview(now func() (int64, error)) (*ElapsedReview, error) {
	tick, err := now()
	if err != nil || tick < 0 {
		return nil, fmt.Errorf("lifecycle elapsed clock unavailable: %v", err)
	}
	return &ElapsedReview{now: now, first: tick, last: tick}, nil
}
func (r *ElapsedReview) Check() error {
	if r == nil {
		return fmt.Errorf("retained lifecycle elapsed review required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return r.failure
	}
	tick, err := r.now()
	if err != nil || tick < r.last || tick < 0 {
		r.failure = fmt.Errorf("lifecycle elapsed clock failed or regressed: %v", err)
		return r.failure
	}
	r.last = tick
	if tick-r.first >= int64(ReviewTTL) {
		r.failure = fmt.Errorf("lifecycle review expired in elapsed time")
		return r.failure
	}
	return nil
}

type elapsedPermits struct {
	mu     sync.Mutex
	now    func() (int64, error)
	last   int64
	have   bool
	failed error
	issued map[string]int64
	wall   time.Time
}

func (r *elapsedPermits) tick() (int64, error) {
	if r.failed != nil {
		return 0, r.failed
	}
	if r.now == nil {
		r.failed = fmt.Errorf("lifecycle elapsed clock unavailable")
		return 0, r.failed
	}
	tick, err := r.now()
	if err != nil || tick < 0 || r.have && tick < r.last {
		r.failed = fmt.Errorf("lifecycle elapsed clock failed or regressed: %v", err)
		return 0, r.failed
	}
	r.last = tick
	r.have = true
	return tick, nil
}
func (r *elapsedPermits) issue(digest string, observed time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tick, err := r.tick()
	if err != nil {
		return err
	}
	if r.issued == nil {
		r.issued = map[string]int64{}
	}
	// The same digest never gains a later lifetime, even with frozen wall time.
	if first, ok := r.issued[digest]; ok {
		if tick-first >= int64(ReviewTTL) {
			return fmt.Errorf("exact lifecycle review has expired; prepare a distinct fresh review")
		}
		return nil
	}
	// A frozen/rolled-back wall clock cannot re-create a purged old digest.
	if !r.wall.IsZero() && !observed.After(r.wall) {
		return fmt.Errorf("fresh lifecycle review requires advancing wall audit time")
	}
	for digest, first := range r.issued {
		if tick-first >= int64(ReviewTTL) {
			delete(r.issued, digest)
		}
	}
	if len(r.issued) >= MaxRecords {
		return fmt.Errorf("too many pending lifecycle reviews; wait for expiry")
	}
	r.wall = observed
	r.issued[digest] = tick
	return nil
}
func (r *elapsedPermits) check(digest string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tick, err := r.tick()
	if err != nil {
		return err
	}
	first, ok := r.issued[digest]
	if !ok || tick-first >= int64(ReviewTTL) {
		return fmt.Errorf("exact lifecycle review is unavailable or expired; prepare a fresh review")
	}
	return nil
}

// clockTick checks conversion rather than allowing timespec arithmetic wrap.
func clockTick(sec, nsec int64) (int64, error) {
	if sec < 0 || nsec < 0 || nsec >= int64(time.Second) || sec > ((1<<63-1)-nsec)/int64(time.Second) {
		return 0, fmt.Errorf("invalid lifecycle elapsed clock sample")
	}
	return sec*int64(time.Second) + nsec, nil
}
