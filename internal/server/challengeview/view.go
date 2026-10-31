// Package challengeview exposes only the actual nonclosing authenticated phase
// view. It has no constructor, issuer, native grant, secret, parent or closer.
package challengeview

import (
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/server/internal/residentservice"
)

type CurrentChallengeView = residentservice.CurrentChallengeView
type CurrentChallengeData = residentprotocol.CurrentChallengeBodyData
