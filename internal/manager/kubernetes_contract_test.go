package manager

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/kubesource"
)

func TestKubernetesSourceContractSQLProjectionIsBoundedAndExact(t *testing.T) {
	for _, mode := range []string{"missing", "canonical", "altered-same-length", "short-text", "appended-text", "oversized-text", "canonical-blob", "oversized-blob", "null", "integer"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			// Deliberately permit NULL/wrong SQLite types in this malformed schema
			// fixture. Production's NOT NULL column cannot create the NULL control.
			if _, err = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value)`); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
			case "oversized-text":
				_, err = db.Exec(`INSERT INTO metadata VALUES(?,CAST(zeroblob(?) AS TEXT))`, kubesource.ContractKey, 8<<20)
			case "oversized-blob":
				_, err = db.Exec(`INSERT INTO metadata VALUES(?,zeroblob(?))`, kubesource.ContractKey, 8<<20)
			default:
				var value any = kubesource.ContractValue
				switch mode {
				case "altered-same-length":
					value = strings.Replace(kubesource.ContractValue, `"version":2`, `"version":3`, 1)
				case "short-text":
					value = kubesource.ContractValue[:len(kubesource.ContractValue)-1]
				case "appended-text":
					value = kubesource.ContractValue + "x"
				case "canonical-blob":
					value = []byte(kubesource.ContractValue)
				case "null":
					value = nil
				case "integer":
					value = 2
				}
				_, err = db.Exec(`INSERT INTO metadata VALUES(?,?)`, kubesource.ContractKey, value)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Existing values must never be rewritten, even when valid. Triggers make
			// mutation observable without reading the oversized payload into Go.
			if mode != "missing" {
				for _, q := range []string{`CREATE TRIGGER refuse_contract_insert BEFORE INSERT ON metadata BEGIN SELECT RAISE(ABORT,'existing contract INSERT forbidden'); END`, `CREATE TRIGGER refuse_contract_update BEFORE UPDATE ON metadata BEGIN SELECT RAISE(ABORT,'existing contract UPDATE forbidden'); END`, `CREATE TRIGGER refuse_contract_delete BEFORE DELETE ON metadata BEGIN SELECT RAISE(ABORT,'existing contract DELETE forbidden'); END`} {
					if _, err = db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			type observation struct {
				Kind   string
				Size   sql.NullInt64
				Prefix sql.NullString
			}
			observe := func() observation {
				t.Helper()
				var v observation
				err := tx.QueryRow(`SELECT typeof(value),length(CAST(value AS BLOB)),hex(substr(CAST(value AS BLOB),1,?)) FROM metadata WHERE key=?`, len(kubesource.ContractValue)+1, kubesource.ContractKey).Scan(&v.Kind, &v.Size, &v.Prefix)
				if mode == "missing" && err == sql.ErrNoRows {
					return v
				}
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			before := observe()
			err = ensureKubernetesSourceContractTx(tx, sourceCoreScope(kubesource.Start))
			wantSuccess := mode == "missing" || mode == "canonical"
			if (err == nil) != wantSuccess {
				t.Fatal("contract acceptance differs", mode, err)
			}
			after := observe()
			if mode != "missing" && !reflect.DeepEqual(before, after) {
				t.Fatal("contract metadata was changed", before, after)
			}
			if wantSuccess && (after.Kind != "text" || !after.Size.Valid || after.Size.Int64 != int64(len(kubesource.ContractValue))) {
				t.Fatal("canonical existing/new value changed", after)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKubernetesSourceLegacyProfileDoesNotRequireContractTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, stage := range []byte{kubesource.Ship, kubesource.Inspect} {
		if err = ensureKubernetesSourceContractTx(tx, sourceCoreScope(stage)); err != nil {
			t.Fatal("unchanged profile queried new metadata", stage, err)
		}
	}
}
