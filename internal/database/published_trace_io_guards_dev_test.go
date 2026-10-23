//go:build arm_devtrace

package database_test

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedFormalInputGuards(t *testing.T) {
	root, err := filepath.Abs("../../.tla")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "input-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "input.json")
	valid := publishedFormalState{Schema: "arm.published-agents/v1"}
	raw, _ := json.Marshal([]publishedFormalState{valid, valid})
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var states []publishedFormalState
	if err = publishedFormalReadJSON(path, &states); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"duplicate":  []byte(`[{"schema":"x","schema":"y"}]`),
		"case-alias": []byte(`[{"Schema":"arm.published-agents/v1"}]`),
		"missing":    []byte(`[{}]`), "null": []byte(`[null]`),
		"trailing": append(append([]byte(nil), raw...), []byte(` []`)...),
		"oversize": make([]byte, (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err = os.WriteFile(path, input, 0600); err != nil {
				t.Fatal(err)
			}
			if err = publishedFormalReadJSON(path, &states); err == nil {
				t.Fatal("unsafe formal evidence accepted")
			}
		})
	}
	link := filepath.Join(dir, "link.json")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(dir, "alias")
	if err = os.Symlink(dir, ancestor); err != nil {
		t.Fatal(err)
	}
	if _, err = publishedFormalPath(filepath.Join(ancestor, "nested.json")); err == nil {
		t.Fatal("followed ancestor symlink")
	}
	if err = publishedFormalReadJSON(link, &states); err == nil {
		t.Fatal("followed input symlink")
	}
	if err = publishedFormalWriteJSON(link, valid); err == nil {
		t.Fatal("followed output symlink")
	}
	out := filepath.Join(dir, "output.json")
	if err = publishedFormalWriteJSON(out, valid); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private output required", st, err)
	}
	if err = publishedFormalWriteJSON(out, valid); err == nil {
		t.Fatal("overwrote retained formal output")
	}
	if _, err = publishedFormalPath(filepath.Join(dir, "..", "..", "outside.json")); err == nil {
		t.Fatal("formal output escaped artifact namespace")
	}
}

// Guard every raw key before the observer maps producer rows to symbolic IDs.
func TestPublishedFormalRawKeyGuards(t *testing.T) {
	f := newPublishedFormalFixture(t)
	for _, a := range []string{"publish1", "prepare", "begin", "accept1"} {
		f.step(t, a)
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(f.targetHome, "state.db")}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for table, columns := range map[string][]string{
		"published_agents":        {"workspace", "endpoint", "generation", "key"},
		"published_agent_changes": {"workspace"},
		"routed_agent_links":      {"workspace", "message", "local_message"},
	} {
		for _, column := range columns {
			for _, kind := range []string{"nul", "blob", "retarget"} {
				t.Run(table+"/"+column+"/"+kind, func(t *testing.T) {
					tx, e := db.Begin()
					if e != nil {
						t.Fatal(e)
					}
					defer tx.Rollback()
					if e = publishedFormalKeys(tx, table); e != nil {
						t.Fatal("genuine keys refused", e)
					}
					value := column + "||char(0)||'suffix'"
					if kind == "blob" {
						value = "CAST(" + column + " AS BLOB)"
					}
					if kind == "retarget" {
						value = "'foreign'"
					}
					if _, e = tx.Exec("UPDATE " + table + " SET " + column + "=" + value); e != nil {
						t.Fatal(e)
					}
					if e = publishedFormalKeys(tx, table); e == nil {
						t.Fatal("raw changed key normalized")
					}
				})
			}
		}
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE published_agent_changes SET sequence=1.5`); e != nil {
		t.Fatal(e)
	}
	if e = publishedFormalKeys(tx, "published_agent_changes"); e == nil {
		t.Fatal("noninteger journal sequence normalized")
	}
}
