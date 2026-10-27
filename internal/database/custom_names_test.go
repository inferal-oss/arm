package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const customTestHost = "0123456789abcdef0123456789abcdef"

func customRaw(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(proofCustomNames{Version: 1, Owner: strconv.Itoa(os.Geteuid()), Host: customTestHost, Revision: 9, Entries: []proofCustomName{{Kind: "resource", AuthorityHost: customTestHost, ID: customTestHost + "/ports/host/tcp4/127.0.0.1:8080", BindingLease: strings.Repeat("a", 32), Name: "preview-http", Active: false}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestCustomNamesIndependentInspectionBoundsTypesAndCorruption(t *testing.T) {
	_, db := fixture(t)
	owner := strconv.Itoa(os.Geteuid())
	valid := customRaw(t)
	absent, err := InspectCustomNames(t.Context(), db, owner, customTestHost)
	if err != nil || absent.Present || !absent.Valid {
		t.Fatal(absent, err)
	}
	for _, tt := range []struct {
		name  string
		value any
		valid bool
	}{
		{"valid", valid, true}, {"empty", "", false}, {"blob", []byte(valid), false}, {"number", 19, false}, {"oversize", strings.Repeat("x", proofCustomNamesBytes+1), false},
		{"case", strings.Replace(valid, `"active":false`, `"Active":false`, 1), false},
		{"duplicate", strings.Replace(valid, `"active":false`, `"active":false,"active":true`, 1), false},
		{"null", strings.Replace(valid, `"binding_lease":"`+strings.Repeat("a", 32)+`"`, `"binding_lease":null`, 1), false},
		{"owner", strings.Replace(valid, `"owner":"`+owner+`"`, `"owner":"999999999"`, 1), false},
		{"host", strings.Replace(valid, `"host":"`+customTestHost+`"`, `"host":"`+strings.Repeat("b", 32)+`"`, 1), false},
		{"missing-incarnation", strings.Replace(valid, strings.Repeat("a", 32), "", 1), false},
		{"entries-null", strings.Replace(valid, strings.Split(valid, `"entries":`)[1], "null}", 1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := db.Exec(`INSERT INTO metadata(key,value) VALUES('custom_names_v1',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, tt.value); err != nil {
				t.Fatal(err)
			}
			before := 0
			if err := db.QueryRow(`SELECT length(CAST(value AS BLOB)) FROM metadata WHERE key='custom_names_v1'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			r, err := InspectCustomNames(t.Context(), db, owner, customTestHost)
			if err != nil || !r.Present || r.Valid != tt.valid {
				t.Fatal(r, err)
			}
			if !tt.valid && (r.Diagnostic == nil || r.Diagnostic.Code != "name_metadata_invalid") {
				t.Fatal("missing fixed cosmetic finding", r)
			}
			if tt.valid && (r.Revision != 9 || r.Entries != 1 || r.Diagnostic != nil) {
				t.Fatal(r)
			}
			after := 0
			if err := db.QueryRow(`SELECT length(CAST(value AS BLOB)) FROM metadata WHERE key='custom_names_v1'`).Scan(&after); err != nil || before != after {
				t.Fatal("inspection modified metadata", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectCustomNames(ctx, db, owner, customTestHost); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation converted to cosmetic finding", err)
	}
	if _, err := InspectCustomNames(t.Context(), db, owner, strings.Repeat("b", 32)); err == nil {
		t.Fatal("wrong source authority accepted")
	}
	if _, err := db.Exec(`DROP TABLE metadata`); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectCustomNames(t.Context(), db, owner, customTestHost); err == nil {
		t.Fatal("SQL failure converted to cosmetic finding")
	}
}
func TestCustomNamesProofDefersHostUntilCompleteMetadataScan(t *testing.T) {
	_, db := fixture(t)
	raw := customRaw(t)
	if _, err := db.Exec(`DELETE FROM metadata`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metadata VALUES('custom_names_v1',?),('host',?)`, raw, customTestHost); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p := proofReader{ctx: t.Context(), tx: tx, catalog: map[string][]string{"metadata": {"key", "value"}}, digest: sha256.New(), result: SettledProof{Owner: strconv.Itoa(os.Geteuid()), Tables: map[string]int64{}}}
	if err = p.metadata(); err != nil {
		t.Fatal(err)
	}
	if p.result.Host != customTestHost || p.customNamesRaw == nil {
		t.Fatal("metadata scan lost authority/custom value")
	}
	if err = p.readCustomNames(); err != nil {
		t.Fatal("dangling cosmetic reservation refused", err)
	}
	if p.result.Complete || p.result.Settled || len(p.result.Blockers) != 0 {
		t.Fatal("cosmetic metadata granted settlement credit")
	}
	wrong := strings.Replace(raw, `"host":"`+customTestHost+`"`, `"host":"`+strings.Repeat("b", 32)+`"`, 1)
	p.customNamesRaw = &wrong
	if p.readCustomNames() == nil {
		t.Fatal("deferred authority mismatch accepted")
	}
}
func TestCustomNamesCheckAndBackupKeepCosmeticFailureSeparate(t *testing.T) {
	home, db := fixture(t)
	raw := `{"private-malformed-label":"never echo this"}`
	if _, err := db.Exec(`INSERT INTO metadata VALUES('custom_names_v1',?)`, raw); err != nil {
		t.Fatal(err)
	}
	before := tree(t, home)
	source, err := Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	check, err := CheckDatabase(t.Context(), source, true)
	if err != nil || !check.OK || !check.Complete || check.CustomNames == nil || check.CustomNames.Valid || check.CustomNames.Diagnostic == nil {
		t.Fatal(check, err)
	}
	if !reflect.DeepEqual(before, tree(t, home)) {
		t.Fatal("readonly check changed original files")
	}
	backupDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(backupDir, "qualified-backup.db")
	backup, err := BackupDatabase(t.Context(), source, target)
	if err != nil || backup.CustomNames == nil || backup.CustomNames.Valid || backup.CustomNames.Diagnostic == nil {
		t.Fatal(backup, err)
	}
	copy, err := openRO(target)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var copied string
	if err = copy.QueryRow(`SELECT value FROM metadata WHERE key='custom_names_v1'`).Scan(&copied); err != nil || copied != raw {
		t.Fatal("backup repaired/dropped cosmetic bytes", copied, err)
	}
	if !reflect.DeepEqual(before, tree(t, home)) {
		t.Fatal("backup changed original files")
	}
	report, _ := json.Marshal([]any{check, backup})
	if bytes.Contains(report, []byte("never echo this")) {
		t.Fatal("diagnostic leaked raw private metadata")
	}
}

func TestCustomNamesIndependentHostNamespaceAndIncarnation(t *testing.T) {
	doc := proofCustomNames{Version: 1, Owner: strconv.Itoa(os.Geteuid()), Host: customTestHost, Revision: 1, Entries: []proofCustomName{{Kind: "host", AuthorityHost: customTestHost, ID: customTestHost, Name: "worker", Active: true}, {Kind: "host", AuthorityHost: strings.Repeat("a", 32), ID: strings.Repeat("a", 32), Name: "WORKER", Active: false}}}
	raw, _ := json.Marshal(doc)
	if _, err := decodeProofCustomNames(string(raw), doc.Owner, doc.Host); err == nil {
		t.Fatal("proof accepted duplicate host name across target authorities")
	}
	doc.Entries = []proofCustomName{{Kind: "resource", AuthorityHost: customTestHost, ID: customTestHost + "/ports/host/tcp4/127.0.0.1:8080", BindingLease: strings.Repeat("a", 32), Name: "old-listener", Active: true}, {Kind: "resource", AuthorityHost: customTestHost, ID: customTestHost + "/ports/host/tcp4/127.0.0.1:8080", BindingLease: strings.Repeat("b", 32), Name: "new-listener", Active: true}}
	raw, _ = json.Marshal(doc)
	if _, err := decodeProofCustomNames(string(raw), doc.Owner, doc.Host); err != nil {
		t.Fatal("proof conflated separate port lease incarnations", err)
	}
	doc.Entries[1].Name = "old-listener"
	raw, _ = json.Marshal(doc)
	if _, err := decodeProofCustomNames(string(raw), doc.Owner, doc.Host); err == nil {
		t.Fatal("proof allowed name reuse without exact-binding clear")
	}
}

func TestCustomNamesInspectionRefusesPhysicalKeyAliasesAndSourceMixups(t *testing.T) {
	_, db := fixture(t)
	owner := strconv.Itoa(os.Geteuid())
	raw := customRaw(t)
	for _, mode := range []string{"blob-only", "text-plus-blob", "oversized-blob"} {
		t.Run(mode, func(t *testing.T) {
			if _, err := db.Exec(`DELETE FROM metadata WHERE CAST(key AS BLOB)=CAST('custom_names_v1' AS BLOB)`); err != nil {
				t.Fatal(err)
			}
			value := raw
			if mode == "oversized-blob" {
				value = strings.Repeat("x", proofCustomNamesBytes+1)
			}
			if _, err := db.Exec(`INSERT INTO metadata VALUES(CAST('custom_names_v1' AS BLOB),?)`, value); err != nil {
				t.Fatal(err)
			}
			if mode == "text-plus-blob" {
				if _, err := db.Exec(`INSERT INTO metadata VALUES('custom_names_v1',?)`, raw); err != nil {
					t.Fatal(err)
				}
			}
			report, err := InspectCustomNames(t.Context(), db, owner, customTestHost)
			if err != nil || !report.Present || report.Valid || report.Diagnostic == nil {
				t.Fatal("physical alias was hidden as absence", report, err)
			}
		})
	}
	if _, err := db.Exec(`DELETE FROM metadata WHERE CAST(key AS BLOB)=CAST('custom_names_v1' AS BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metadata VALUES('custom_names_v1',?)`, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE metadata SET value=? WHERE key='host'`, strings.Repeat("b", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectCustomNames(t.Context(), db, owner, customTestHost); err == nil {
		t.Fatal("metadata from another source accepted because document host matched")
	}
}
