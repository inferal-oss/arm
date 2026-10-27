package cli

import (
	"fmt"

	"getarm.org/internal/model"
)

func (a *app) poolHierarchy(action string, args []string) (int, error) {
	f := a.flags("pool " + action)
	f.Bool("json", false, "JSON hierarchy page with exact integral quantities")
	q := model.PoolTreeQuery{}
	f.IntVar(&q.Limit, "limit", 50, "page size1..100")
	f.StringVar(&q.After, "after", "", "resume after immutable ID")
	if action == "consumers" {
		f.BoolVar(&q.Descendants, "descendants", false, "include actual direct claims through descendant pools; overlapping ancestor totals are not additive")
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if action != "roots" {
		q.ID = f.Args()[0]
	}
	op := "pool_children"
	if action == "consumers" {
		op = "pool_consumers"
	}
	var result any
	if err := a.call(op, q, &result); err != nil {
		return 1, err
	}
	return 0, a.print(result)
}

func (a *app) poolLookup(args []string) (int, error) {
	f := a.flags("pool lookup")
	f.Bool("json", false, "JSON durable operation lookup")
	key := f.String("key", "", "exact durable pool operation key")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if *key == "" || len(*key) > 128 {
		return 2, fmt.Errorf("--key must contain1..128 bytes")
	}
	var result any
	if err := a.call("pool_lookup", map[string]string{"idempotency_key": *key}, &result); err != nil {
		return 1, err
	}
	return 0, a.print(result)
}
