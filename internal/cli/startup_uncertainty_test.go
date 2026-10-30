package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func TestExistingStartupFailedInspectionIsUnknown(t *testing.T) {
	for _, failure := range []string{"malformed-endpoint", "unreadable-endpoint", "failed-status"} {
		t.Run(failure, func(t *testing.T) {
			home := t.TempDir()
			endpoint := filepath.Join(home, "endpoint.json")
			switch failure {
			case "malformed-endpoint":
				if err := os.WriteFile(endpoint, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable-endpoint":
				// A directory gives a deterministic read failure even for root.
				if err := os.Mkdir(endpoint, 0700); err != nil {
					t.Fatal(err)
				}
			case "failed-status":
				// Port zero cannot name a listening service. No unknown/default
				// backend or detached process is started by this regression.
				if err := local.AtomicJSON(endpoint, local.Endpoint{URL: "ws://127.0.0.1:0/v1/ws", Token: "startup-fixture", Generation: strings.Repeat("1", 32)}); err != nil {
					t.Fatal(err)
				}
			}
			a := app{home: home, ctx: t.Context()}
			_, running, err := a.existingStartup()
			var detail *model.Error
			if running || !errors.As(err, &detail) || detail.Code != "startup_outcome_unknown" {
				t.Fatalf("failed inspection authorized absence: running=%v, error=%v; want startup_outcome_unknown", running, err)
			}
			for _, name := range []string{"state.db", "daemon.lock", "daemon.log"} {
				if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
					t.Fatalf("inspection created %s: %v", name, err)
				}
			}
		})
	}
}
