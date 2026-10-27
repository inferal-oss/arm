package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/historypack"
	"github.com/inferal-oss/arm/internal/sshservice"
	"golang.org/x/sys/unix"
)

const sourceHistoryProofName = "ssh-source-history"
const sourceHistoryProofBytes int64 = 1 << 30

var sourceHistoryProofDDL = []string{
	`CREATE TABLE source_history_metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
	`CREATE TABLE source_history_operations(key TEXT PRIMARY KEY,request TEXT NOT NULL,phase TEXT NOT NULL,data TEXT NOT NULL)`,
	`CREATE TABLE source_history_packs(id TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE source_history_refs(source TEXT NOT NULL,target TEXT NOT NULL,key TEXT NOT NULL,kind TEXT NOT NULL,pack TEXT NOT NULL,record TEXT NOT NULL,sha256 TEXT NOT NULL,intent_sha256 TEXT NOT NULL,PRIMARY KEY(source,target,key))`,
}

func strictHistoryProof(raw []byte, out any, limit int) error {
	if len(raw) > limit {
		return fmt.Errorf("source history record exceeds independent bound")
	}
	if e := preflightHistoryProof(raw); e != nil {
		return e
	}
	if e := strictEvidence(string(raw), out); e != nil {
		return e
	}
	v, e := json.Marshal(out)
	if e != nil || !bytes.Equal(raw, v) {
		return fmt.Errorf("source history differs from closed canonical proof")
	}
	return nil
}

// Reject large arrays before strictEvidence materializes typed fields. New
// source records have at most64 reviewed members/cleanup names/audit attempts.
func preflightHistoryProof(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("source history JSON depth exceeds bound")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		switch v := t.(type) {
		case json.Delim:
			if v != '{' && v != '[' {
				return fmt.Errorf("invalid history delimiter")
			}
			n := 0
			for d.More() {
				n++
				if n > 64 {
					return fmt.Errorf("source history collection exceeds64 before allocation")
				}
				if v == '{' {
					key, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := key.(string)
					if !ok || len(s) > 128 {
						return fmt.Errorf("history field exceeds bound")
					}
				}
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			want := json.Delim(']')
			if v == '{' {
				want = '}'
			}
			if e != nil || end != want {
				return fmt.Errorf("history collection incomplete")
			}
		case nil:
			return fmt.Errorf("null source history evidence")
		case string:
			if len(v) > 8192 {
				return fmt.Errorf("source history string exceeds bound")
			}
		case json.Number:
			if len(v) > 20 {
				return fmt.Errorf("source history number exceeds bound")
			}
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing source history JSON")
	}
	return nil
}
func historyProofID(v File) proofHistoryIdentity {
	return proofHistoryIdentity{v.Device, v.Inode, v.Owner, v.Mode}
}
func historyProofIdentity(v proofHistoryIdentity, owner string, dir bool) bool {
	mode := uint32(unix.S_IFREG | 0600)
	if dir {
		mode = unix.S_IFDIR | 0700
	}
	return v.Inode != 0 && v.Owner == uint32Owner(owner) && v.Mode == mode
}
func historyProofText(s string, limit int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(s) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func validHistoryMarker(m proofHistoryMarker, host, owner string) bool {
	return m.Schema == 1 && m.Binding.Host == host && m.Binding.Owner == owner && historyProofText(m.Binding.Account, 256) && validHost(m.CatalogID) && historyProofIdentity(m.Directory, owner, true) && historyProofIdentity(m.Packs, owner, true) && historyProofIdentity(m.Catalog, owner, false) && historyProofIdentity(m.WorkerLock, owner, false)
}
func proofHistoryKeyFor(m proofHistoryMember) proofHistoryKey {
	domain := "ssh-source-completion-v1"
	if m.Kind == "seal" {
		domain = "ssh-source-seal-v1"
	}
	return proofHistoryKey{domain, m.Source + "/" + m.Target, m.Key}
}
func proofHistoryHash(v any) string {
	raw, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return proofDigest(raw)
}
func validateHistoryOperation(op proofHistoryOperation, m proofHistoryMarker) error {
	q, p := op.Request, op.Request.Plan
	copy := p
	copy.SHA256 = ""
	if op.Schema != 1 || q.Schema != 1 || !historyProofText(q.Key, 128) || !historyProofText(q.Reason, 4096) || op.RequestSHA256 != proofHistoryHash(q) || op.Actor.UID != m.Binding.Owner || op.Actor.Username != m.Binding.Account || op.Created.IsZero() || op.Updated.Before(op.Created) || op.Revision < 1 || !validHost(op.PackID) || !oneOf(op.Phase, "prepared", "pack_prepared", "published", "committed", "completed", "needs_attention") || op.Error != "" && !historyProofText(op.Error, 4096) || op.Cleaned == nil || len(op.Cleaned) > len(p.Members) || p.Schema != 1 || p.Binding != m.Binding || !validDigest(p.Certificate) || p.CatalogID != "" && p.CatalogID != m.CatalogID || !validDigest(p.RosterSHA256) || !validDigest(p.SHA256) || p.SHA256 != proofHistoryHash(copy) || p.Selection.Target != "" && !validHost(p.Selection.Target) || p.Selection.Limit < 1 || p.Selection.Limit > 64 || len(p.Members) < 1 || len(p.Members) > p.Selection.Limit || p.HotBefore < 0 || p.HotBefore > 64 || p.ColdBefore < 0 || p.ColdBefore > 16384 || p.Bytes < 1 || p.Bytes > 64*(32<<10) {
		return fmt.Errorf("source archive operation authority/request/phase invalid")
	}
	last := ""
	var size int64
	names := map[string]bool{}
	for _, v := range p.Members {
		tuple := v.Source + "\x00" + v.Target + "\x00" + v.Key
		prefix := "ssh-complete-"
		if v.Kind == "seal" {
			prefix = "ssh-sealed-"
		}
		if tuple <= last || v.Source != m.Binding.Host || !validHost(v.Target) || !historyProofText(v.Key, 128) || !oneOf(v.Kind, "completion", "seal") || v.File != prefix+proofDigest([]byte(tuple))+".json" || !historyProofIdentity(v.Identity, m.Binding.Owner, false) || !validDigest(v.SHA256) || !validDigest(v.IntentSHA256) || v.Bytes < 1 || v.Bytes > 32<<10 || (v.PendingFile == "") != (v.PendingIdentity == nil) || (v.PendingFile == "") != (v.PendingSHA256 == "") {
			return fmt.Errorf("source archive member differs")
		}
		if v.PendingFile != "" && (v.PendingFile != "ssh-client-"+proofDigest([]byte(tuple))+".json" || !historyProofIdentity(*v.PendingIdentity, m.Binding.Owner, false) || v.PendingSHA256 != v.IntentSHA256) {
			return fmt.Errorf("source pending duplicate differs")
		}
		names[v.File] = true
		last = tuple
		size += v.Bytes
	}
	if size != p.Bytes {
		return fmt.Errorf("source archive byte count differs")
	}
	cleaned := map[string]bool{}
	for _, name := range op.Cleaned {
		if !names[name] || cleaned[name] {
			return fmt.Errorf("source cleanup roster differs")
		}
		cleaned[name] = true
	}
	if op.Phase == "completed" && len(cleaned) != len(names) {
		return fmt.Errorf("source completed archive lacks cleanup evidence")
	}
	if op.Prepared != nil && (op.Prepared.ID != op.PackID || op.Prepared.Authority != m.Binding.Host || op.Prepared.Root != m.Packs) {
		return fmt.Errorf("source prepared pack belongs elsewhere")
	}
	if op.Receipt != nil && (op.Prepared == nil || *op.Receipt != *op.Prepared) {
		return fmt.Errorf("source publication receipt differs")
	}
	if oneOf(op.Phase, "pack_prepared", "published", "committed", "completed") && op.Prepared == nil || oneOf(op.Phase, "published", "committed", "completed") && op.Receipt == nil {
		return fmt.Errorf("source phase lost born publication evidence")
	}
	return nil
}
func validateHistoryRecovery(raw []byte, m proofHistoryMarker) ([]ProofBlocker, error) {
	if raw == nil {
		return nil, nil
	}
	var v proofHistoryRecovery
	if e := strictHistoryProof(raw, &v, 64<<10); e != nil {
		return nil, e
	}
	if v.Schema != 1 || v.Marker != m || v.Attempts == nil || len(v.Attempts) < 1 || len(v.Attempts) > 64 {
		return nil, fmt.Errorf("source recovery authority/roster differs")
	}
	seen := map[string]bool{}
	last := time.Time{}
	for i, a := range v.Attempts {
		if !historyProofText(a.Key, 128) || seen[a.Key] || !historyProofText(a.Reason, 4096) || a.Actor.UID != m.Binding.Owner || a.Actor.Username != m.Binding.Account || !historyProofIdentity(a.Journal, m.Binding.Owner, false) || a.JournalBytes <= 0 || a.JournalBytes > (64<<20)+4096 || !validDigest(a.JournalSHA256) || a.Started.IsZero() || a.Started.Before(last) || (a.Completed == nil) != (a.Result == nil) || a.Completed == nil && i != len(v.Attempts)-1 {
			return nil, fmt.Errorf("source recovery original audit invalid")
		}
		if a.Completed == nil {
			return []ProofBlocker{{"ssh_history_recovery", a.Key, "Exact catalog recovery remains pending; a removed journal is not settlement"}}, nil
		}
		if a.Completed.Before(a.Started) || a.Result.CatalogBytes < 1 || a.Result.CatalogBytes > 64<<20 || !validDigest(a.Result.CatalogSHA256) || a.Result.Packs < 0 || a.Result.Packs > 256 || a.Result.Archived < 0 || a.Result.Archived > 16384 {
			return nil, fmt.Errorf("source recovery completed evidence invalid")
		}
		seen[a.Key] = true
		last = a.Started
	}
	return nil, nil
}
func plainHistoryLock(d *Directory, name string, expected proofHistoryIdentity) (*os.File, error) {
	f, v, e := d.Open(name, unix.O_RDONLY)
	if e != nil || f == nil {
		return nil, fmt.Errorf("source history coordination lock missing/unsafe")
	}
	if v.Size != 0 || historyProofID(v) != expected {
		f.Close()
		return nil, fmt.Errorf("source history coordination lock identity changed")
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("source history coordination busy: %w", e)
	}
	now, e := d.Observe(name, false)
	if e != nil || historyProofID(now) != expected {
		unlockReset(f)
		return nil, fmt.Errorf("source history named lock changed")
	}
	return f, nil
}
func (r *SettledReview) reviewSSHSourceHistory(ctx context.Context, mesh *Directory, contents map[string][]byte) error {
	d, e := r.child(sourceHistoryProofName)
	if e != nil {
		return e
	}
	raw := contents["ssh-history.json"]
	if raw == nil && d == nil {
		if contents["ssh-history-recovery.json"] != nil {
			return fmt.Errorf("source recovery lost catalog marker")
		}
		return nil
	}
	if raw == nil || d == nil {
		return fmt.Errorf("source history marker/root incomplete")
	}
	var m proofHistoryMarker
	if e = strictHistoryProof(raw, &m, 32<<10); e != nil || !validHistoryMarker(m, r.Proof.Host, r.Proof.Owner) || historyProofID(d.identity) != m.Directory {
		return fmt.Errorf("source history marker authority/identity invalid: %v", e)
	}
	// Existing lifecycle lock is held. Never wait for either subordinate lock.
	sshLock, lockID, e := mesh.Open("ssh-pins.lock", unix.O_RDONLY)
	if e != nil || sshLock == nil || lockID.Size != 0 {
		return fmt.Errorf("source SSH lock missing/unsafe")
	}
	if e = unix.Flock(int(sshLock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		sshLock.Close()
		return e
	}
	r.locks = append(r.locks, sshLock)
	worker, e := plainHistoryLock(d, ".worker.lock", m.WorkerLock)
	if e != nil {
		return e
	}
	r.locks = append(r.locks, worker)
	blockers, e := validateHistoryRecovery(contents["ssh-history-recovery.json"], m)
	if e != nil {
		return e
	}
	r.Proof.Blockers = append(r.Proof.Blockers, blockers...)
	if len(blockers) > 0 {
		return fmt.Errorf("source catalog recovery incomplete; preserve namespaces")
	}
	evidence, files, e := collectSSHHistoryEvidence(ctx, d, m)
	if e != nil {
		return e
	}
	if e = proveSSHHistoryCatalog(ctx, d, m, evidence, contents); e != nil {
		return e
	}
	r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: sourceHistoryProofName, Present: true, Identity: d.identity, Files: files, SSHHistory: evidence})
	return nil
}

func historyProofFile(ctx context.Context, d *Directory, name string, limit int64) (File, error) {
	f, v, e := d.Open(name, unix.O_RDONLY)
	if e != nil || f == nil {
		return v, fmt.Errorf("source history file missing/unsafe")
	}
	defer f.Close()
	if v.Mode != unix.S_IFREG|0600 || v.Size < 0 || v.Size > limit {
		return v, fmt.Errorf("source history file exceeds exact mode/size policy")
	}
	v.SHA256, e = hashFile(ctx, f)
	if e != nil {
		return v, e
	}
	now, e := d.Observe(name, false)
	if e != nil || !sameIdentity(v, now) || v.Size != now.Size || v.Modified != now.Modified || v.Changed != now.Changed {
		return v, fmt.Errorf("source history file changed while hashing")
	}
	return v, nil
}
func collectSSHHistoryEvidence(ctx context.Context, d *Directory, m proofHistoryMarker) (*SSHSourceHistoryEvidence, []File, error) {
	e := &SSHSourceHistoryEvidence{Packs: []SSHSourceHistoryPackEvidence{}}
	files := []File{}
	names, err := resetNames(d, 3)
	if err != nil || !reflect.DeepEqual(names, []string{".worker.lock", "catalog.db", "packs"}) {
		return nil, nil, fmt.Errorf("source catalog unknown sidecar/partial roster")
	}
	for _, name := range []string{".worker.lock", "catalog.db"} {
		v, err := historyProofFile(ctx, d, name, 64<<20)
		if err != nil {
			return nil, nil, err
		}
		want := m.Catalog
		if name == ".worker.lock" {
			want = m.WorkerLock
			if v.Size != 0 {
				return nil, nil, fmt.Errorf("source worker lock not empty")
			}
		}
		if historyProofID(v) != want {
			return nil, nil, fmt.Errorf("source catalog born file changed")
		}
		files = append(files, v)
		e.Bytes += v.Size
	}
	root, err := resetChild(d, "packs", false)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if historyProofID(root.identity) != m.Packs {
		return nil, nil, fmt.Errorf("source packs root identity changed")
	}
	e.PacksRoot = root.identity
	e.PacksRoot.Name = "packs"
	names, err = resetNames(root, 257)
	if err != nil {
		return nil, nil, err
	}
	e.Lock = File{Name: ".historypack.lock"}
	for _, name := range names {
		if name == ".historypack.lock" {
			v, err := historyProofFile(ctx, root, name, 0)
			if err != nil {
				return nil, nil, err
			}
			e.Lock = v
			continue
		}
		if !strings.HasPrefix(name, "pack-") || !validHost(strings.TrimPrefix(name, "pack-")) || len(e.Packs) >= 256 {
			return nil, nil, fmt.Errorf("source pack is staged/unfamiliar/oversized; no settlement inferred")
		}
		pack, err := resetChild(root, name, false)
		if err != nil {
			return nil, nil, err
		}
		p := SSHSourceHistoryPackEvidence{Directory: pack.identity, Files: []File{}}
		p.Directory.Name = name
		children, err := resetNames(pack, 2)
		if err != nil || !reflect.DeepEqual(children, []string{"manifest.json", "records.bin"}) {
			pack.Close()
			return nil, nil, fmt.Errorf("source pack file roster differs")
		}
		var size int64
		for _, child := range children {
			v, err := historyProofFile(ctx, pack, child, 3<<20)
			if err != nil {
				pack.Close()
				return nil, nil, err
			}
			size += v.Size
			p.Files = append(p.Files, v)
		}
		if size > 3<<20 {
			pack.Close()
			return nil, nil, fmt.Errorf("source pack combined bound exceeded")
		}
		if err = pack.Validate(); err != nil {
			pack.Close()
			return nil, nil, err
		}
		pack.Close()
		e.Bytes += size
		if e.Bytes > sourceHistoryProofBytes {
			return nil, nil, fmt.Errorf("source history exceeds1GiB complete proof budget")
		}
		e.Packs = append(e.Packs, p)
	}
	if len(e.Packs) > 0 && !e.Lock.Present {
		return nil, nil, fmt.Errorf("source pack publication lock missing")
	}
	if err = d.Validate(); err != nil {
		return nil, nil, err
	}
	return e, files, nil
}
func proveSSHHistoryCatalog(ctx context.Context, d *Directory, m proofHistoryMarker, evidence *SSHSourceHistoryEvidence, hot map[string][]byte) error {
	db, e := openRO(filepath.Join(d.Path, "catalog.db"))
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, `PRAGMA max_page_count=16384`); e != nil {
		return e
	}
	rows, e := db.QueryContext(ctx, `SELECT type,name,tbl_name,substr(sql,1,4097) FROM sqlite_schema ORDER BY name LIMIT 9`)
	if e != nil {
		return e
	}
	tables := map[string]string{}
	indexes := map[string]bool{}
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if e = rows.Scan(&kind, &name, &table, &ddl); e != nil {
			rows.Close()
			return e
		}
		if kind == "table" && ddl.Valid {
			tables[name] = ddl.String
		} else if kind == "index" && !ddl.Valid && name == "sqlite_autoindex_"+table+"_1" {
			indexes[table] = true
		} else {
			rows.Close()
			return fmt.Errorf("unknown source history schema object")
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	if len(tables) != 4 || len(indexes) != 4 {
		return fmt.Errorf("source history DDL incomplete")
	}
	for _, ddl := range sourceHistoryProofDDL {
		parts := strings.Fields(ddl)
		name := strings.Split(parts[2], "(")[0]
		if tables[name] != ddl || !indexes[name] {
			return fmt.Errorf("source history independent DDL differs")
		}
	}
	var pageSize, pages int
	var mode string
	if e = db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); e != nil {
		return e
	}
	if e = db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); e != nil {
		return e
	}
	if e = db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); e != nil {
		return e
	}
	if pageSize != 4096 || pages < 1 || pages > 16384 || mode != "delete" {
		return fmt.Errorf("source catalog SQLite policy/bounds differ")
	}
	rows, e = db.QueryContext(ctx, `SELECT substr(key,1,65),substr(value,1,16385) FROM source_history_metadata LIMIT 3`)
	if e != nil {
		return e
	}
	meta := map[string]string{}
	for rows.Next() {
		var k, v string
		if e = rows.Scan(&k, &v); e != nil {
			rows.Close()
			return e
		}
		meta[k] = v
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	marker, _ := json.Marshal(m)
	if len(meta) != 2 || meta["schema"] != "1" || meta["marker"] != string(marker) {
		return fmt.Errorf("source catalog metadata/marker differs")
	}
	ops := map[string]proofHistoryOperation{}
	byPack := map[string]proofHistoryOperation{}
	expected := map[string]proofHistoryMember{}
	rows, e = db.QueryContext(ctx, `SELECT substr(key,1,129),substr(request,1,262145),substr(phase,1,65),substr(data,1,262145) FROM source_history_operations ORDER BY key LIMIT 257`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var key, request, phase, raw string
		if e = rows.Scan(&key, &request, &phase, &raw); e != nil {
			rows.Close()
			return e
		}
		var op proofHistoryOperation
		if len(ops) >= 256 || strictHistoryProof([]byte(raw), &op, 256<<10) != nil || validateHistoryOperation(op, m) != nil || key != op.Request.Key || phase != op.Phase || request != stringJSONHistory(op.Request) {
			rows.Close()
			return fmt.Errorf("source archive original operation invalid")
		}
		if phase != "completed" {
			rows.Close()
			return fmt.Errorf("source archive remains incomplete; cannot retire")
		}
		if _, exists := byPack[op.PackID]; exists {
			rows.Close()
			return fmt.Errorf("source pack used by multiple operations")
		}
		ops[key] = op
		byPack[op.PackID] = op
		for _, v := range op.Request.Plan.Members {
			tuple := v.Source + "\x00" + v.Target + "\x00" + v.Key
			if _, ok := expected[tuple]; ok {
				rows.Close()
				return fmt.Errorf("duplicate source key")
			}
			expected[tuple] = v
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	var first *proofHistoryOperation
	for _, op := range ops {
		if op.Request.Plan.CatalogID == "" {
			if first != nil {
				return fmt.Errorf("multiple catalog initialization plans")
			}
			v := op
			first = &v
		}
	}
	if first != nil {
		for _, op := range ops {
			if op.Created.Before(first.Created) {
				return fmt.Errorf("source initialization plan not first")
			}
		}
	}
	packs := map[string]proofHistoryPack{}
	rows, e = db.QueryContext(ctx, `SELECT substr(id,1,33),substr(data,1,262145) FROM source_history_packs ORDER BY id LIMIT 257`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return e
		}
		var p proofHistoryPack
		op, ok := byPack[id]
		if len(packs) >= 256 || !ok || strictHistoryProof([]byte(raw), &p, 256<<10) != nil || p.Schema != 1 || p.Receipt.ID != id || p.Operation != op.Request.Key || p.Members != len(op.Request.Plan.Members) || op.Receipt == nil || p.Receipt != *op.Receipt || p.Receipt.Root != m.Packs || p.Receipt.RecordsBytes > 3<<20 || p.Receipt.ManifestBytes > (3<<20)-p.Receipt.RecordsBytes {
			rows.Close()
			return fmt.Errorf("source pack row/original receipt differs")
		}
		packs[id] = p
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	if len(packs) != len(ops) || len(packs) != len(evidence.Packs) || len(expected) > 16384 {
		return fmt.Errorf("source catalog complete pack/operation roster differs")
	}
	seen := map[string]bool{}
	rows, e = db.QueryContext(ctx, `SELECT substr(source,1,33),substr(target,1,33),substr(key,1,129),substr(kind,1,33),substr(pack,1,33),substr(record,1,4097),substr(sha256,1,65),substr(intent_sha256,1,65) FROM source_history_refs ORDER BY source,target,key LIMIT 16385`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var source, target, key, kind, pack, record, sha, intentSHA string
		if e = rows.Scan(&source, &target, &key, &kind, &pack, &record, &sha, &intentSHA); e != nil {
			rows.Close()
			return e
		}
		tuple := source + "\x00" + target + "\x00" + key
		v, ok := expected[tuple]
		op, packOK := byPack[pack]
		var k proofHistoryKey
		member := false
		for _, x := range op.Request.Plan.Members {
			if x.Source == source && x.Target == target && x.Key == key {
				member = true
			}
		}
		if !ok || seen[tuple] || !packOK || !member || strictHistoryProof([]byte(record), &k, 4096) != nil || k != proofHistoryKeyFor(v) || kind != v.Kind || sha != v.SHA256 || intentSHA != v.IntentSHA256 {
			rows.Close()
			return fmt.Errorf("source permanent key ref differs from original operation")
		}
		seen[tuple] = true
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	if len(seen) != len(expected) {
		return fmt.Errorf("source permanent key fences missing")
	}
	for _, pe := range evidence.Packs {
		id := strings.TrimPrefix(pe.Directory.Name, "pack-")
		row, ok := packs[id]
		if !ok || historyProofID(pe.Directory) != row.Receipt.Directory || historyProofID(evidence.Lock) != row.Receipt.Lock {
			return fmt.Errorf("source pack native roster differs")
		}
		if e = proveSSHHistoryPayloads(ctx, filepath.Join(d.Path, "packs"), row, byPack[id], hot); e != nil {
			return e
		}
	}
	return d.Validate()
}
func stringJSONHistory(v any) string {
	raw, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return string(raw)
}
func proveSSHHistoryPayloads(ctx context.Context, root string, row proofHistoryPack, op proofHistoryOperation, hot map[string][]byte) error {
	raw, _ := json.Marshal(row.Receipt)
	var receipt historypack.Receipt
	if e := json.Unmarshal(raw, &receipt); e != nil {
		return e
	}
	pack, e := historypack.Open(ctx, root, receipt)
	if e != nil {
		return e
	}
	defer pack.Close()
	manifest, e := pack.Manifest()
	if e != nil {
		return e
	}
	if len(manifest.Entries) != row.Members {
		return fmt.Errorf("source pack contains unreviewed/missing records")
	}
	for _, m := range op.Request.Plan.Members {
		k := proofHistoryKeyFor(m)
		payload, e := pack.Read(ctx, historypack.Key{Domain: k.Domain, Principal: k.Principal, Value: k.Value})
		if e != nil {
			return e
		}
		if int64(len(payload)) != m.Bytes || proofDigest(payload) != m.SHA256 {
			return fmt.Errorf("source payload differs from reviewed bytes")
		}
		var intent sshservice.SourceIntent
		if m.Kind == "completion" {
			var closed proofSSHCompletion
			if e = strictSSHProducer(payload, &closed); e != nil {
				return e
			}
			var v sshservice.SourceCompletion
			if e = json.Unmarshal(payload, &v); e != nil || v.Validate() != nil {
				return fmt.Errorf("cold source completion lacks terminal settlement")
			}
			intent = v.Intent
		} else {
			var closed proofSSHSourceSeal
			if e = strictSSHProducer(payload, &closed); e != nil {
				return e
			}
			var v sshservice.SourceSealCompletion
			if e = json.Unmarshal(payload, &v); e != nil || v.Validate() != nil {
				return fmt.Errorf("cold source seal lacks nonexecution settlement")
			}
			intent = v.Intent
		}
		if intent.Source != m.Source || intent.Request.Pin.Target != m.Target || intent.Request.Key != m.Key || proofHistoryHash(intent) != m.IntentSHA256 {
			return fmt.Errorf("cold source original intent differs")
		}
		if _, e = os.Lstat(intent.Directory.Path); !os.IsNotExist(e) {
			return fmt.Errorf("cold source invocation artifacts remain or cannot be proved absent")
		}
		if original, ok := hot[m.File]; ok && !bytes.Equal(original, payload) {
			return fmt.Errorf("hot/cold source receipts contradict")
		}
		name := "ssh-client-" + proofDigest([]byte(m.Source+"\x00"+m.Target+"\x00"+m.Key)) + ".json"
		if original, ok := hot[name]; ok && proofDigest(original) != m.IntentSHA256 {
			return fmt.Errorf("cold source receipt/pending original contradict")
		}
	}
	return pack.Verify(ctx)
}
