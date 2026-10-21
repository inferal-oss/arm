package local

import (
	"context"
	"os"
	"os/user"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

// Identity keeps the kernel UID as the authority. Username is an optional
// display snapshot from the OS account database, never an environment value.
type Identity struct {
	UID      string
	Username string
}

type identityResolver struct{ slots chan struct{} }

var accountResolver = identityResolver{slots: make(chan struct{}, 1)}

func CurrentIdentity() Identity {
	uid := strconv.Itoa(os.Geteuid())
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	return accountResolver.resolve(ctx, uid, user.LookupId)
}

// Directory services may block indefinitely. Bound the caller's wait and allow
// only one outstanding native lookup in this process, even after a timeout.
func (r *identityResolver) resolve(ctx context.Context, uid string, lookup func(string) (*user.User, error)) Identity {
	identity := Identity{UID: uid}
	select {
	case <-ctx.Done():
		return identity
	default:
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return identity
	}
	type result struct {
		user *user.User
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		defer func() { <-r.slots }()
		u, err := lookup(uid)
		resultCh <- result{u, err}
	}()
	select {
	case got := <-resultCh:
		if got.err == nil && got.user != nil && got.user.Uid == uid && validUsername(got.user.Username) {
			identity.Username = got.user.Username
		}
	case <-ctx.Done():
	}
	return identity
}

func validUsername(name string) bool {
	return name != "" && len(name) <= 256 && utf8.ValidString(name) && !stringsContainControl(name)
}

func stringsContainControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
