// Package historypack stores bounded immutable evidence bytes. A valid pack is
// consistency evidence only: it never establishes settlement, authentication,
// cleanup authority or permission to reuse an execution key.
package historypack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Schema            = 1
	MaxRecords        = 4096
	MaxPayloadBytes   = 1 << 20
	MaxManifestBytes  = 4 << 20
	MaxPackBytes      = 64 << 20 // records.bin plus manifest.json
	MaxHeaderBytes    = 8 << 10
	MaxHeadersBytes   = 4 << 20
	MaxDomainBytes    = 64
	MaxPrincipalBytes = 256
	MaxKeyBytes       = 1024
)

// Key is compared exactly, without trimming, case folding or normalization.
// Principal may be empty for local-only domains. Value and Domain may not.
type Key struct {
	Domain    string `json:"domain"`
	Principal string `json:"principal"`
	Value     string `json:"key"`
}
type Record struct {
	Key     Key
	Payload []byte
}
type Identity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Owner  uint32 `json:"owner"`
	Mode   uint32 `json:"mode"`
}
type Entry struct {
	Key    Key    `json:"key"`
	Offset uint64 `json:"frame_offset"`
	Size   uint64 `json:"payload_bytes"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Schema        int     `json:"schema_version"`
	ID            string  `json:"pack_id"`
	Authority     string  `json:"authority"`
	RecordsBytes  uint64  `json:"records_bytes"`
	RecordsSHA256 string  `json:"records_sha256"`
	Entries       []Entry `json:"entries"`
}

// Prepared is born-directory evidence which the caller must persist durably
// BEFORE Publish. A partial Prepare error may still return its born identities.
// No failure automatically removes a stage or an unfamiliar namespace member.
type Prepared struct {
	ID             string   `json:"pack_id"`
	Authority      string   `json:"authority"`
	Root           Identity `json:"root"`
	Lock           Identity `json:"lock"`
	Directory      Identity `json:"directory"`
	Records        Identity `json:"records"`
	Manifest       Identity `json:"manifest"`
	RecordsBytes   uint64   `json:"records_bytes"`
	ManifestBytes  uint64   `json:"manifest_bytes"`
	RecordsSHA256  string   `json:"records_sha256"`
	ManifestSHA256 string   `json:"manifest_sha256"`
}

// Receipt preserves the same born identities through publication. Repeating
// Publish with the exact Prepared value can inspect an already-published pack.
type Receipt struct{ Prepared }

// PublicationError means rename succeeded or its durability/canonical path
// cannot be certified. Receipt is retained for exact inspection, never resend
// of an execution or permission to delete either name.
type PublicationError struct {
	Receipt Receipt
	Err     error
}

func (e *PublicationError) Error() string {
	return "history pack publication uncertain: " + e.Err.Error()
}
func (e *PublicationError) Unwrap() error { return e.Err }

type frameHeader struct {
	Key    Key    `json:"key"`
	SHA256 string `json:"sha256"`
}

func validText(s string, limit int, empty bool) bool {
	if !utf8.ValidString(s) || len(s) > limit || (!empty && s == "") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func (k Key) valid() bool {
	return validText(k.Domain, MaxDomainBytes, false) && validText(k.Principal, MaxPrincipalBytes, true) && validText(k.Value, MaxKeyBytes, false)
}
func compare(a, b Key) int {
	if n := strings.Compare(a.Domain, b.Domain); n != 0 {
		return n
	}
	if n := strings.Compare(a.Principal, b.Principal); n != 0 {
		return n
	}
	return strings.Compare(a.Value, b.Value)
}
func hexValue(s string, size int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == size && strings.ToLower(s) == s
}
func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func canonical(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	encoded, e := json.Marshal(out)
	if e != nil || !bytes.Equal(encoded, raw) {
		return fmt.Errorf("noncanonical/unknown/missing/null/duplicate evidence fields")
	}
	return nil
}
func stageName(id string) string { return ".stage-" + id }
func finalName(id string) string { return "pack-" + id }
func (p Prepared) valid() bool {
	return hexValue(p.ID, 16) && hexValue(p.Authority, 16) && hexValue(p.ManifestSHA256, 32) && hexValue(p.RecordsSHA256, 32) && p.RecordsBytes >= uint64(len(magic)) && p.ManifestBytes > 0 && p.ManifestBytes <= MaxManifestBytes && p.RecordsBytes <= MaxPackBytes && p.ManifestBytes <= MaxPackBytes-p.RecordsBytes && validDir(p.Root) && validFile(p.Lock) && validDir(p.Directory) && validFile(p.Records) && validFile(p.Manifest)
}
