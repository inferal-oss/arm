package database

import (
	"encoding/json"
	"getarm.org/internal/model"
	"strings"
	"testing"
)

func TestPoolReviewIndependentConsumerShape(t *testing.T) {
	p := proofReader{result: SettledProof{Host: "a"}, poolPaths: map[string][]string{"p": {"p"}}}
	good := `{"reviewed":{"authority":"a","consumers":[{"id":"l","revision":1}],"hierarchy":{"schema_version":1,"pool":"p","root":"p","pool_count":1,"consumer_count":1,"snapshot_sha256":"` + strings.Repeat("a", 64) + `"}}}`
	q := model.PoolMutation{ID: "p", Operation: "set"}
	if e := p.validatePoolReviewRecord(good, q); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(good, `"authority":"a"`, `"authority":"a","unavailable":null`, 1), strings.Replace(good, `"id":"l"`, `"ID":"l"`, 1), strings.Replace(good, `"revision":1`, `"revision":null`, 1), strings.Replace(good, `,"revision":1`, "", 1), strings.Replace(good, `"revision":1`, `"revision":1,"future":true`, 1), strings.Replace(good, `"consumers":[{"id":"l","revision":1}]`, `"consumers":null`, 1)} {
		if !json.Valid([]byte(bad)) {
			t.Fatal("bad fixture")
		}
		if e := p.validatePoolReviewRecord(bad, q); e == nil {
			t.Fatal("widened independent consumer", bad)
		}
	}
}
