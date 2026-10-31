package agentprojects

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"getarm.org/internal/agentsessions"
)

type titleFixture struct {
	result   agentsessions.TitleResult
	roots    []string
	deadline time.Time
	calls    int
}

func (f *titleFixture) DiscoverTitles(ctx context.Context, roots []string) agentsessions.TitleResult {
	f.roots = roots
	f.calls++
	f.deadline, _ = ctx.Deadline()
	return f.result
}

func TestProjectTitlesCatalogBudgets(t *testing.T) {
	base := t.TempDir()
	home := projectDir(t, base, "client")
	roots := []string{}
	for i := 0; i < 17; i++ {
		roots = append(roots, projectDir(t, base, fmt.Sprintf("root-%02d", i)))
	}
	writeJSON(t, home+"/.codex-global-state.json", map[string]any{"active-workspace-roots": roots})
	fixture := &titleFixture{result: agentsessions.TitleResult{Titles: []agentsessions.Title{{Directory: roots[16], Name: "Later worktree task", Updated: time.Now()}}}}
	for i := 0; i < agentsessions.Limit+1; i++ {
		fixture.result.Titles = append(fixture.result.Titles, agentsessions.Title{Directory: roots[0], Name: fmt.Sprintf("Name %03d", i), Updated: time.Now()})
	}
	start := time.Now()
	got := (WithTitles{Adapter: Codex{Home: home}, Source: fixture}).Discover(t.Context())
	if len(fixture.roots) != 17 || fixture.calls != 1 || len(got.Projects) != 17 || len(got.Projects[0].Names) != agentsessions.Limit-1 || !reflect.DeepEqual(got.Projects[16].Names, []string{"Later worktree task"}) || len(got.Warnings) != 0 || fixture.deadline.IsZero() || fixture.deadline.After(start.Add(9*time.Second)) {
		t.Fatal("catalog budget changed", len(fixture.roots), len(got.Projects), len(got.Projects[0].Names), got.Warnings, fixture.deadline)
	}
}

func TestProjectTitlesExactAssociationSharedDirectoriesAndFallback(t *testing.T) {
	base := t.TempDir()
	home := projectDir(t, base, "client")
	root := projectDir(t, base, "generic")
	child := projectDir(t, root, "nested")
	writeJSON(t, home+"/.codex-global-state.json", map[string]any{"active-workspace-roots": []string{root, child}})
	plain := Discover(t.Context(), []Adapter{Codex{Home: home}})
	now := time.Now()
	fixture := &titleFixture{result: agentsessions.TitleResult{Titles: []agentsessions.Title{
		{Directory: root, Name: "Zulu", Updated: now},
		{Directory: root, Name: "Alpha", Updated: now},
		{Directory: root, Name: "Alpha", Updated: now},
		{Directory: child, Name: "Old child title", Updated: now.Add(-31 * 24 * time.Hour)},
		{Directory: root, Name: "Undated"},
		{Directory: root, Name: "Future", Updated: now.Add(time.Hour)},
		{Directory: base, Name: "Parent is not a match", Updated: now},
		{Directory: root + "/.", Name: "Not exact", Updated: now},
	}}}
	got := Discover(t.Context(), []Adapter{WithTitles{Adapter: Codex{Home: home}, Source: fixture}})
	if !reflect.DeepEqual(got.Projects[0].Names, []string{"Alpha", "Zulu"}) || !reflect.DeepEqual(got.Projects[1].Names, []string{"Old child title"}) {
		t.Fatal(got)
	}
	for i := range got.Projects {
		got.Projects[i].Names = nil
	}
	if !reflect.DeepEqual(got, plain) || !reflect.DeepEqual(fixture.roots, []string{root, child}) {
		t.Fatal("names changed directory discovery", got, plain, fixture.roots)
	}
	fixture.result = agentsessions.TitleResult{Warnings: []string{"Metadata unavailable"}}
	fallback := (WithTitles{Adapter: Codex{Home: home}, Source: fixture}).Discover(t.Context())
	if len(fallback.Projects) != 2 || len(fallback.Projects[0].Names) != 0 || len(fallback.Warnings) != 1 {
		t.Fatal(fallback)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := Discover(ctx, []Adapter{WithTitles{Adapter: Codex{Home: home}, Source: fixture}}); len(got.Projects) != 0 {
		t.Fatal(got)
	}
}

func TestProjectTitlesSanitizeAndBoundRendering(t *testing.T) {
	got := SanitizeTitle("  Fix\nstatus\x1b[2J\u202e\t" + strings.Repeat("界", 300))
	if len([]rune(got)) > 257 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "Fix status [2J ") {
		t.Fatal(got)
	}
	for _, r := range got {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			t.Fatal("control in title", got)
		}
	}
	if SanitizeTitle("\n\x1b\u202e\t") != "" || SanitizeTitle(strings.Repeat("\u202e", 300)) != "" {
		t.Fatal("control-only title retained")
	}
}
