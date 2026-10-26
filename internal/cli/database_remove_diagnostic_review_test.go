package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestReviewRemoveMissingPressureSchemaHumanAndJSONAreReadOnly(t *testing.T) {
	for _, machine := range []bool{false, true} {
		name := "human"
		if machine {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			home := resetCLIState(t, false, false)
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"host_pressure_state", "host_pressure_outbox"} {
				if _, err := db.Exec(`DROP TABLE ` + name); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := resetCLITree(t, home)
			var out, errout bytes.Buffer
			a := app{home: home, ctx: context.Background(), in: strings.NewReader("yes\n"), out: &out, err: &errout,
				outputTTY: !machine, errorTTY: !machine, inputTTY: !machine, forceJSON: machine}
			code, removeErr := a.database([]string{"remove", "--reason", "wipe"})
			if code == 0 || removeErr == nil {
				t.Fatal("incompatible disposable ledger reached removal confirmation")
			}
			a.reportError(removeErr)
			message := errout.String()
			if machine {
				var result struct {
					Error model.Error `json:"error"`
				}
				if err := json.Unmarshal(errout.Bytes(), &result); err != nil || result.Error.Code == "" {
					t.Fatal("missing structured refusal", errout.String(), err)
				}
				message = result.Error.Message
			}
			if !strings.Contains(message, "host_pressure_") || strings.Contains(message, "\x1f") || strings.Contains(message, `\u001f`) {
				t.Errorf("internal schema tokens leaked into %s diagnostic: %q", name, message)
			}
			if out.Len() != 0 {
				t.Error("incomplete proof presented a removal plan or confirmation", out.String())
			}
			if after := resetCLITree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("schema refusal changed files, receipts, locks, modes or database bytes")
			}
		})
	}
}
