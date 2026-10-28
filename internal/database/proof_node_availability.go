package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"getarm.org/internal/mesh"
)

// Prepared exact walk hooks: use this limit inside the SQL CASE projection,
// then guard the scanned typeof/length triples before constructing/digesting r.
func nodeAvailabilityProofReadBound(table, column string) string {
	if table == "metadata" && column == "value" {
		return `(CASE WHEN "key"='node_availability_v1' THEN 8192 ELSE 1048576 END)`
	}
	return "1048576"
}

func guardNodeAvailabilityMetadataRow(table string, columns []string, values []any) error {
	if table != "metadata" {
		return nil
	}
	key, value := slices.Index(columns, "key"), slices.Index(columns, "value")
	if key < 0 || value < 0 || len(values) != len(columns)*3 {
		return fmt.Errorf("availability proof metadata columns differ from supported projection")
	}
	if values[key*3+2] != "node_availability_v1" {
		return nil
	}
	kind, ok := values[value*3].(string)
	size, sized := values[value*3+1].(int64)
	raw, text := values[value*3+2].(string)
	if !ok || kind != "text" || !sized || size < 0 || size > 8<<10 || !text || int64(len(raw)) != size {
		return fmt.Errorf("availability proof metadata has unsupported type or exceeds8KiB")
	}
	return nil
}

// Independent closed producer mirror. This validates one cosmetic metadata key;
// it neither settles a lease nor supplies authority to forget any obligation.
// The metadata switch must explicitly opt in after independent schema review.
func (p *proofReader) nodeAvailabilityMetadata(raw string) error {
	var host string
	var kind string
	var size int64
	if err := p.tx.QueryRowContext(p.ctx, `SELECT typeof(value),coalesce(length(CAST(value AS BLOB)),-1),CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))=32 THEN value ELSE '' END FROM metadata WHERE key='host'`).Scan(&kind, &size, &host); err != nil {
		return err
	}
	if kind != "text" || size != 32 || len(host) != 32 {
		return fmt.Errorf("availability proof host authority is not bounded canonical TEXT")
	}
	return validateNodeAvailabilityMetadata(raw, host, p.result.Owner)
}

func validateNodeAvailabilityMetadata(raw, host, owner string) error {
	if len(raw) > 8<<10 || !utf8.ValidString(raw) {
		return fmt.Errorf("availability metadata exceeds proof bounds")
	}
	var d struct {
		Schema     int       `json:"schema"`
		Host       string    `json:"host"`
		Owner      string    `json:"owner"`
		Revision   uint64    `json:"revision"`
		Profile    string    `json:"profile"`
		Reason     string    `json:"reason"`
		UpdatedAt  time.Time `json:"updated_at"`
		LastUpdate struct {
			Key              string `json:"key"`
			ExpectedRevision uint64 `json:"expected_revision"`
			Profile          string `json:"profile"`
			Reason           string `json:"reason"`
		} `json:"last_update"`
	}
	if err := strictEvidence(raw, &d, "schema", "host", "owner", "revision", "profile", "reason", "updated_at", "last_update"); err != nil {
		return err
	}
	canonical, err := json.Marshal(d)
	if err != nil || !bytes.Equal(canonical, []byte(raw)) {
		return fmt.Errorf("availability metadata differs from canonical proof producer")
	}
	if !validHost(host) || d.Host != host || d.Owner != owner || d.Schema != 1 || d.Revision < 1 || d.LastUpdate.ExpectedRevision != d.Revision-1 || d.UpdatedAt.IsZero() || d.UpdatedAt.Location() != time.UTC || !mesh.ValidKey(d.LastUpdate.Key) || d.LastUpdate.Profile != d.Profile || d.LastUpdate.Reason != d.Reason || d.Reason == "" || len(d.Reason) > 1024 || strings.TrimSpace(d.Reason) != d.Reason || strings.ContainsRune(d.Reason, 0) || (d.Profile != "unspecified" && d.Profile != "stable" && d.Profile != "intermittent") {
		return fmt.Errorf("availability cosmetic metadata differs from stopped host/owner or supported schema")
	}
	return nil
}
