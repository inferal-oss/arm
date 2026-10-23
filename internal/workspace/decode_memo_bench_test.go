package workspace

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
	"unicode/utf8"
)

type decodeBenchRecord struct {
	name string
	raw  []byte
	typ  reflect.Type
}

// Records come from real Store PrepareCreate/Decide/Issue and exact SQLite raw
// rows, plus a real Pages output. Setup/certificate creation/SQL are outside the
// timed region: these benchmarks isolate JSON validation+fresh typed decode.
func decodeBenchRecords(tb testing.TB) []decodeBenchRecord {
	tb.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	hostBytes := make([]byte, 16)
	if _, e := rand.Read(hostBytes); e != nil {
		tb.Fatal(e)
	}
	host := hex.EncodeToString(hostBytes)
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		tb.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{"node-" + host + ".arm.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if e != nil {
		tb.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		tb.Fatal(e)
	}
	fingerprint := sha256.Sum256(der)
	spki := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	self := Identity{Host: host, Name: "decode benchmark", Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(fingerprint[:]), SPKI: hex.EncodeToString(spki[:]), Expires: parsed.NotAfter}
	db, e := sql.Open("sqlite", filepath.Join(tb.TempDir(), "workspace.db"))
	if e != nil {
		tb.Fatal(e)
	}
	tb.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`); e != nil {
		tb.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO metadata(key,value) VALUES('host',?)`, host); e != nil {
		tb.Fatal(e)
	}
	ctx := context.Background()
	a := Authority{Host: host, UID: strconv.Itoa(os.Geteuid())}
	clock := &fakeClock{wall: now}
	store, e := Open(ctx, db, a, self, clock)
	if e != nil {
		tb.Fatal(e)
	}
	op, e := store.PrepareCreate(ctx, CreateRequest{Key: "decode-benchmark", Name: "real JSON decode fixture", Policy: DefaultPolicy(), Reason: "benchmark strict JSON independently of policy decisions"})
	if e != nil {
		tb.Fatal(e)
	}
	if _, e = store.Decide(ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); e != nil {
		tb.Fatal(e)
	}
	records, e := store.Read(ctx)
	if e != nil {
		tb.Fatal(e)
	}
	root := records.Instances[0]
	snap, e := store.Issue(ctx, RefreshRequest{Source: rootPrincipal(root), Nonce: hash("decode-benchmark")})
	if e != nil {
		tb.Fatal(e)
	}
	pages, e := Pages(snap, MaxPageBytes)
	if e != nil {
		tb.Fatal(e)
	}
	var out []decodeBenchRecord
	for _, q := range []struct {
		name, table string
		typ         reflect.Type
	}{{"Instance", "workspace_instances", reflect.TypeFor[Instance]()}, {"Operation", "workspace_operations", reflect.TypeFor[Operation]()}, {"Change", "workspace_changes", reflect.TypeFor[Change]()}} {
		var raw []byte
		if e = db.QueryRow(`SELECT data FROM ` + q.table + ` ORDER BY rowid DESC LIMIT 1`).Scan(&raw); e != nil {
			tb.Fatal(e)
		}
		out = append(out, decodeBenchRecord{q.name, raw, q.typ})
	}
	raw, e := json.Marshal(pages[0])
	if e != nil {
		tb.Fatal(e)
	}
	return append(out, decodeBenchRecord{"Page", raw, reflect.TypeFor[Page]()})
}

// Exact pre-change Decode algorithm from clean HEAD7be8e45. The strict unique/
// fields functions are unchanged. This test-only baseline caches nothing.
func decodeMemoLegacyBaseline(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxRecordBytes || !utf8.Valid(raw) {
		return fmt.Errorf("invalid bounded UTF-8 workspace record")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := unique(d, 0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing workspace JSON")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("workspace decoder requires struct pointer")
	}
	if e := fields(raw, t.Elem()); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(out)
}
func BenchmarkDecodeMemoRealRecords(b *testing.B) {
	records := decodeBenchRecords(b)
	for _, record := range records {
		b.Run(record.name, func(b *testing.B) {
			b.Run("LegacyCold", func(b *testing.B) {
				b.SetBytes(int64(len(record.raw)))
				b.ReportAllocs()
				for range b.N {
					out := reflect.New(record.typ).Interface()
					if e := decodeMemoLegacyBaseline(record.raw, out); e != nil {
						b.Fatal(e)
					}
				}
			})
			b.Run("MemoColdMiss", func(b *testing.B) {
				m := memoForTest()
				out := reflect.New(record.typ).Interface()
				if e := decodeWithMemo(record.raw, out, m); e != nil {
					b.Fatal(e)
				}
				b.SetBytes(int64(len(record.raw)))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					m.mu.Lock()
					clear(m.entries)
					clear(m.order)
					m.first = 0
					m.count = 0
					m.bytes = 0
					m.mu.Unlock()
					b.StartTimer()
					out := reflect.New(record.typ).Interface()
					if e := decodeWithMemo(record.raw, out, m); e != nil {
						b.Fatal(e)
					}
				}
			})
			b.Run("MemoHot", func(b *testing.B) {
				m := memoForTest()
				out := reflect.New(record.typ).Interface()
				if e := decodeWithMemo(record.raw, out, m); e != nil {
					b.Fatal(e)
				}
				b.SetBytes(int64(len(record.raw)))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					out := reflect.New(record.typ).Interface()
					if e := decodeWithMemo(record.raw, out, m); e != nil {
						b.Fatal(e)
					}
				}
			})
		})
	}
}
func TestDecodeMemoRealStoreRowsAndPagesStayFresh(t *testing.T) {
	for _, record := range decodeBenchRecords(t) {
		t.Run(record.name, func(t *testing.T) {
			m := memoForTest()
			one := reflect.New(record.typ).Interface()
			two := reflect.New(record.typ).Interface()
			memoDecode(t, m, record.raw, one)
			memoDecode(t, m, record.raw, two)
			if !reflect.DeepEqual(one, two) {
				t.Fatal("fresh real typed values differ")
			}
			if e := decodeMemoLegacyBaseline(record.raw, reflect.New(record.typ).Interface()); e != nil {
				t.Fatal("baseline disagrees", e)
			}
			if n, _ := memoStats(m); n != 1 {
				t.Fatal("real row not retained", n)
			}
		})
	}
}
