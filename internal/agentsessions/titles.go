package agentsessions

import (
	"context"
	"time"
)

// TitleSource is optional display metadata, separate from session identities.
// Implementations must use bounded read-only metadata, never prompt previews or
// transcripts. Roots are exact canonical directories, not enclosing Git scopes.
type TitleSource interface {
	DiscoverTitles(context.Context, []string) TitleResult
}

type Title struct {
	Directory string
	Name      string
	Updated   time.Time
}

type TitleResult struct {
	Titles   []Title
	Warnings []string
}
