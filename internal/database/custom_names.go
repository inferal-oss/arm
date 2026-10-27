package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"net"
	"os"
	"strconv"
	"strings"
)

// This closed mirror is intentionally independent of the native name writer.
// Optional cosmetic metadata never establishes that a resource is settled.
const proofCustomNamesKey = "custom_names_v1"
const proofCustomNamesBytes = 512 << 10
const proofCustomNamesEntries = 1024

type proofCustomNames struct {
	Version  int               `json:"version"`
	Owner    string            `json:"owner"`
	Host     string            `json:"host"`
	Revision int64             `json:"revision"`
	Entries  []proofCustomName `json:"entries"`
}
type proofCustomName struct {
	Kind          string `json:"kind"`
	AuthorityHost string `json:"authority_host"`
	ID            string `json:"id"`
	BindingLease  string `json:"binding_lease"`
	Name          string `json:"name"`
	Active        bool   `json:"active"`
}

func proofCustomNameValid(name string) bool {
	if len(name) < 1 || len(name) > 128 {
		return false
	}
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }
	if !alnum(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !alnum(name[i]) && name[i] != '.' && name[i] != '_' && name[i] != '-' {
			return false
		}
	}
	lower := strings.ToLower(name)
	return lower != "localhost" && !validHost(lower) && !(strings.HasPrefix(lower, "pool_") && validHost(strings.TrimPrefix(lower, "pool_"))) && !readablename.LooksGeneratedLabel(lower)
}
func proofCustomBindingValid(e proofCustomName) bool {
	if !validHost(e.AuthorityHost) {
		return false
	}
	switch e.Kind {
	case "host":
		return e.ID == e.AuthorityHost && e.BindingLease == ""
	case "lease":
		return validHost(e.ID) && e.BindingLease == ""
	case "pool":
		return strings.HasPrefix(e.ID, "pool_") && validHost(strings.TrimPrefix(e.ID, "pool_")) && e.BindingLease == ""
	case "resource":
		if len(e.ID) > 256 {
			return false
		}
		for _, kind := range []string{"cpu", "memory", "disk"} {
			if e.ID == e.AuthorityHost+"/"+kind {
				return e.BindingLease == ""
			}
		}
		prefix := e.AuthorityHost + "/ports/"
		if !strings.HasPrefix(e.ID, prefix) || !validHost(e.BindingLease) {
			return false
		}
		parts := strings.SplitN(strings.TrimPrefix(e.ID, prefix), "/", 3)
		if len(parts) != 3 || len(parts[0]) < 1 || len(parts[0]) > 128 || !oneOf(parts[1], "tcp4", "tcp6", "udp4", "udp6") {
			return false
		}
		for _, c := range parts[0] {
			if c < 33 || c > 126 {
				return false
			}
		}
		address, port, err := net.SplitHostPort(parts[2])
		ip := net.ParseIP(address)
		n, pErr := strconv.Atoi(port)
		return err == nil && ip != nil && pErr == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port && ip.String() == address && net.JoinHostPort(address, port) == parts[2] && (strings.HasSuffix(parts[1], "4") == (ip.To4() != nil))
	}
	return false
}

// Bound collection allocation before the generic closed-shape proof decoder.
func boundCustomNamesJSON(d *json.Decoder, depth int) error {
	if depth > 8 {
		return fmt.Errorf("custom names nesting exceeds bound")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	count := 0
	switch delim {
	case '{':
		for d.More() {
			if count >= 7 {
				return fmt.Errorf("custom names object exceeds field bound")
			}
			count++
			if _, err = d.Token(); err != nil {
				return err
			}
			if err = boundCustomNamesJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if count >= proofCustomNamesEntries {
				return fmt.Errorf("custom names array exceeds entry bound")
			}
			count++
			if err = boundCustomNamesJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid custom names JSON delimiter")
	}
	_, err = d.Token()
	return err
}

func decodeProofCustomNames(raw, owner, host string) (proofCustomNames, error) {
	var doc proofCustomNames
	if len(raw) > proofCustomNamesBytes {
		return doc, fmt.Errorf("custom names exceeds cosmetic metadata bound")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if err := boundCustomNamesJSON(decoder, 0); err != nil {
		return doc, err
	}
	if err := strictEvidence(raw, &doc); err != nil {
		return doc, err
	}
	uid, err := strconv.ParseUint(owner, 10, 32)
	if err != nil || strconv.FormatUint(uid, 10) != owner || !validHost(host) || doc.Version != 1 || doc.Owner != owner || doc.Host != host || doc.Revision < 1 || doc.Entries == nil || len(doc.Entries) > proofCustomNamesEntries {
		return doc, fmt.Errorf("invalid custom names schema/authority/bounds")
	}
	names, active := map[string]bool{}, map[string]bool{}
	for _, entry := range doc.Entries {
		if !proofCustomNameValid(entry.Name) || !proofCustomBindingValid(entry) {
			return doc, fmt.Errorf("invalid custom name or canonical binding")
		}
		namespace := entry.Kind + "\x00" + entry.AuthorityHost + "\x00"
		if entry.Kind == "host" {
			namespace = "host\x00"
		}
		name := namespace + strings.ToLower(entry.Name)
		binding := entry.Kind + "\x00" + entry.AuthorityHost + "\x00" + entry.ID + "\x00" + entry.BindingLease
		if names[name] || entry.Active && active[binding] {
			return doc, fmt.Errorf("duplicate custom name/binding")
		}
		names[name] = true
		if entry.Active {
			active[binding] = true
		}
	}
	canonical, err := json.Marshal(doc)
	if err != nil || len(canonical) > proofCustomNamesBytes {
		return doc, fmt.Errorf("custom names exceeds encoded metadata bound")
	}
	return doc, nil
}
func (p *proofReader) readCustomNames() error {
	if p.customNamesRaw == nil {
		return nil
	}
	// metadata() has now seen every row, including a host row inserted later.
	_, err := decodeProofCustomNames(*p.customNamesRaw, p.result.Owner, p.result.Host)
	return err
}

type CustomNamesQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type CustomNamesInspection struct {
	Present    bool         `json:"present"`
	Valid      bool         `json:"valid"`
	Revision   int64        `json:"revision,omitempty"`
	Entries    int          `json:"entries,omitempty"`
	Diagnostic *model.Error `json:"diagnostic,omitempty"`
	Scope      string       `json:"scope"`
}

// InspectCustomNames validates only optional cosmetic metadata, independently
// from SQLite structural integrity and provider settlement. Invalid content is
// a bounded finding; SQL/cancellation failures remain operational errors.
func InspectCustomNames(ctx context.Context, q CustomNamesQuerier, owner, host string) (CustomNamesInspection, error) {
	r := CustomNamesInspection{Valid: true, Scope: "optional local presentation metadata only; no existence or settlement authority"}
	if owner != strconv.Itoa(os.Geteuid()) || !validHost(host) {
		return r, fmt.Errorf("authenticated owner and exact source host required")
	}
	// Byte-exact key matching includes physical BLOB aliases. Each CTE is
	// bounded to two candidates: any duplicate is invalid, never an empty map.
	// Bound projected values inside these multiply-used CTEs: SQLite may
	// materialize them before evaluating the outer aggregate expressions.
	const query = `WITH host_rows AS (
 SELECT
 CASE WHEN typeof(key)='text' AND typeof(value)='text' AND length(CAST(value AS BLOB))=32 THEN 1 ELSE 0 END AS supported,
 CASE WHEN typeof(key)='text' AND typeof(value)='text' AND length(CAST(value AS BLOB))=32 THEN value END AS bounded_value
 FROM metadata WHERE CAST(key AS BLOB)=CAST('host' AS BLOB) LIMIT 2
 ), name_rows AS (
 SELECT
 CASE WHEN typeof(key)='text' AND typeof(value)='text' AND length(CAST(value AS BLOB)) BETWEEN 1 AND 524288 THEN 1 ELSE 0 END AS supported,
 CASE WHEN typeof(key)='text' AND typeof(value)='text' AND length(CAST(value AS BLOB)) BETWEEN 1 AND 524288 THEN value END AS bounded_value
 FROM metadata WHERE CAST(key AS BLOB)=CAST('custom_names_v1' AS BLOB) LIMIT 2
 ) SELECT
 (SELECT count(*) FROM host_rows),
 (SELECT coalesce(sum(supported),0) FROM host_rows),
 (SELECT max(bounded_value) FROM host_rows),
 (SELECT count(*) FROM name_rows),
 (SELECT coalesce(sum(supported),0) FROM name_rows),
 (SELECT max(bounded_value) FROM name_rows)`
	var hostCount, hostSupported, count, supported int64
	var sourceHost, raw sql.NullString
	err := q.QueryRowContext(ctx, query).Scan(&hostCount, &hostSupported, &sourceHost, &count, &supported, &raw)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return r, cancelErr
	}
	if err != nil {
		return r, err
	}
	if hostCount != 1 || hostSupported != 1 || !sourceHost.Valid || sourceHost.String != host {
		return r, fmt.Errorf("custom name inspection source authority differs")
	}
	if count == 0 {
		return r, nil
	}
	r.Present = true
	invalid := func() (CustomNamesInspection, error) {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return r, cancelErr
		}
		r.Valid = false
		r.Diagnostic = &model.Error{Code: "name_metadata_invalid", Message: "Optional custom names are invalid; exact IDs remain available. Original metadata was preserved."}
		return r, nil
	}
	if count != 1 || supported != 1 || !raw.Valid {
		return invalid()
	}
	doc, err := decodeProofCustomNames(raw.String, owner, host)
	if err != nil {
		return invalid()
	}
	if err = ctx.Err(); err != nil {
		return r, err
	}
	r.Revision = doc.Revision
	r.Entries = len(doc.Entries)
	return r, nil
}
