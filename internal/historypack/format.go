package historypack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

const magic = "ARM-HISTORY-01\n"
const prefixBytes = 12

func inputOrder(records []Record) ([]Record, error) {
	if len(records) < 1 || len(records) > MaxRecords {
		return nil, fmt.Errorf("pack requires1..4096 records")
	}
	out := append([]Record{}, records...)
	var size uint64 = uint64(len(magic))
	headers := 0
	for _, r := range out {
		if !r.Key.valid() || len(r.Payload) > MaxPayloadBytes {
			return nil, fmt.Errorf("invalid exact key or payload bound")
		}
		h, _ := json.Marshal(frameHeader{r.Key, string(bytes.Repeat([]byte{'0'}, 64))})
		headers += len(h)
		if len(h) > MaxHeaderBytes || headers > MaxHeadersBytes {
			return nil, fmt.Errorf("typed header bound exceeded")
		}
		n := uint64(prefixBytes + len(h) + len(r.Payload))
		if n > MaxPackBytes-size {
			return nil, fmt.Errorf("pack byte bound exceeded")
		}
		size += n
	}
	sort.Slice(out, func(i, j int) bool { return compare(out[i].Key, out[j].Key) < 0 })
	for i := 1; i < len(out); i++ {
		if compare(out[i-1].Key, out[i].Key) == 0 {
			return nil, fmt.Errorf("duplicate exact key")
		}
	}
	return out, nil
}
func makeRecords(ctx context.Context, f *os.File, p *Prepared, records []Record) (Manifest, error) {
	m := Manifest{Schema: Schema, ID: p.ID, Authority: p.Authority, Entries: []Entry{}}
	h := sha256.New()
	write := func(raw []byte) error {
		if e := writeAll(ctx, f, raw); e != nil {
			return e
		}
		h.Write(raw)
		m.RecordsBytes += uint64(len(raw))
		return nil
	}
	if e := write([]byte(magic)); e != nil {
		return m, e
	}
	for _, r := range records {
		// The caller must not mutate supplied records until Prepare returns.
		payload := bytes.Clone(r.Payload)
		sha := digest(payload)
		header, _ := json.Marshal(frameHeader{r.Key, sha})
		entry := Entry{r.Key, m.RecordsBytes, uint64(len(payload)), sha}
		var prefix [prefixBytes]byte
		binary.BigEndian.PutUint32(prefix[:4], uint32(len(header)))
		binary.BigEndian.PutUint64(prefix[4:], uint64(len(payload)))
		for _, raw := range [][]byte{prefix[:], header, payload} {
			if e := write(raw); e != nil {
				return m, e
			}
		}
		m.Entries = append(m.Entries, entry)
	}
	m.RecordsSHA256 = hex.EncodeToString(h.Sum(nil))
	return m, nil
}
func manifestValid(m Manifest) error {
	if m.Schema != Schema || !hexValue(m.ID, 16) || !hexValue(m.Authority, 16) || !hexValue(m.RecordsSHA256, 32) || m.RecordsBytes < uint64(len(magic)) || m.RecordsBytes > MaxPackBytes || len(m.Entries) < 1 || len(m.Entries) > MaxRecords {
		return fmt.Errorf("manifest identity/limits invalid")
	}
	next := uint64(len(magic))
	headers := 0
	for i, e := range m.Entries {
		if !e.Key.valid() || !hexValue(e.SHA256, 32) || e.Size > MaxPayloadBytes || e.Offset != next || i > 0 && compare(m.Entries[i-1].Key, e.Key) >= 0 {
			return fmt.Errorf("noncanonical/overlapping/duplicate entry")
		}
		h, _ := json.Marshal(frameHeader{e.Key, e.SHA256})
		headers += len(h)
		if len(h) > MaxHeaderBytes || headers > MaxHeadersBytes {
			return fmt.Errorf("header bound exceeded")
		}
		n := uint64(prefixBytes+len(h)) + e.Size
		if n > MaxPackBytes-next {
			return fmt.Errorf("offset exceeds bounded pack")
		}
		next += n
	}
	if next != m.RecordsBytes {
		return fmt.Errorf("records EOF differs from manifest")
	}
	return nil
}
func hashFile(ctx context.Context, f *os.File, size uint64) (string, error) {
	if size > MaxPackBytes {
		return "", fmt.Errorf("file size exceeds bound")
	}
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return "", e
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total uint64
	for {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		n, e := f.Read(buf)
		if n > 0 {
			if uint64(n) > size-total {
				return "", fmt.Errorf("trailing records bytes")
			}
			total += uint64(n)
			h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
		if n == 0 {
			return "", io.ErrNoProgress
		}
	}
	if total != size {
		return "", fmt.Errorf("truncated records")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func verifyRecords(ctx context.Context, f *os.File, m Manifest) error {
	if e := manifestValid(m); e != nil {
		return e
	}
	b := make([]byte, len(magic))
	if _, e := f.ReadAt(b, 0); e != nil || string(b) != magic {
		return fmt.Errorf("invalid history framing magic")
	}
	for _, entry := range m.Entries {
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, e := readEntry(f, entry); e != nil {
			return e
		}
	}
	hash, e := hashFile(ctx, f, m.RecordsBytes)
	if e != nil {
		return e
	}
	if hash != m.RecordsSHA256 {
		return fmt.Errorf("records digest differs")
	}
	return nil
}
func readEntry(f *os.File, e Entry) ([]byte, error) {
	var prefix [prefixBytes]byte
	if _, err := f.ReadAt(prefix[:], int64(e.Offset)); err != nil {
		return nil, err
	}
	header, _ := json.Marshal(frameHeader{e.Key, e.SHA256})
	if binary.BigEndian.Uint32(prefix[:4]) != uint32(len(header)) || binary.BigEndian.Uint64(prefix[4:]) != e.Size {
		return nil, fmt.Errorf("frame length differs from canonical manifest")
	}
	actual := make([]byte, len(header))
	if _, err := f.ReadAt(actual, int64(e.Offset)+prefixBytes); err != nil {
		return nil, err
	}
	if !bytes.Equal(actual, header) {
		return nil, fmt.Errorf("frame header differs from manifest")
	}
	payload := make([]byte, int(e.Size))
	if _, err := f.ReadAt(payload, int64(e.Offset)+prefixBytes+int64(len(header))); err != nil {
		return nil, err
	}
	if digest(payload) != e.SHA256 {
		return nil, fmt.Errorf("payload digest differs")
	}
	return payload, nil
}
