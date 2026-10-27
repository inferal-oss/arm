package syncservice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"getarm.org/internal/syncreconcile"
)

const MaxPreparationBytes = MaxRecordBytes + MaxManifestBytes + 1<<20
const decodeReserveBytes int64 = 128 << 10

// The frame allowance covers the source body, RawMessage copies, a decoder
// buffer (which may grow to nearly twice its input), canonical re-encoding,
// and one spare copy. Structural charges below cover Go values separately.
const decodeFrameCopies int64 = 6

// These exceed the current State/map bucket and Action/Resolution slice
// footprints, including map growth and geometric slice spare capacity.
const decodeObjectMemberBytes int64 = 512
const decodeArrayElementBytes int64 = 256
const decodeContainerBytes int64 = 128
const decodeCanonicalCopies int64 = 3
const maxJSONDepth = 64

// DecodeAdmission must be backed by the Manager's live, charged memory hold.
// A resource value copied from the untrusted preparation request is not an
// admission. The worker supplies this before reading a preparation frame.
type DecodeAdmission interface {
	CheckActive(context.Context) error
	MemoryBytes() int64
}

// Preparation is the first length-framed control message on the fixed
// arm sync-worker SSH stream. Binary rsync payload follows only after the
// destination has authenticated, reconstructed, and durably reserved it.
type Preparation struct {
	Version  int           `json:"version"`
	Intent   Intent        `json:"intent"`
	Manifest []ManifestRow `json:"manifest"`
}

func WritePreparation(w io.Writer, p Preparation) error {
	if p.Version != Schema {
		return errors.New("unsupported sync preparation version")
	}
	if err := p.Intent.Validate(); err != nil {
		return err
	}
	manifest, err := json.Marshal(p.Manifest)
	if err != nil {
		return err
	}
	if len(manifest) > MaxManifestBytes || sha(manifest) != p.Intent.ManifestDigest {
		return errors.New("wire manifest differs from reviewed digest")
	}
	if err = validateManifest(p.Intent, p.Manifest); err != nil {
		return err
	}
	record, err := json.Marshal(p.Intent)
	if err != nil {
		return err
	}
	if err = validateFootprint(p.Intent, len(manifest), len(record)); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(raw) > MaxPreparationBytes {
		return errors.New("sync preparation exceeds wire bound")
	}
	var header [8]byte
	binary.BigEndian.PutUint64(header[:], uint64(len(raw)))
	if err = writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, raw)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n < 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// ReadPreparation refuses oversized wire bodies before allocation, then
// streams manifest rows through an explicit count limit before building the
// decoded slice. The caller must call ReserveTx against its local baseline
// before acknowledging preparation or accepting any transfer payload.
func ReadPreparation(ctx context.Context, r io.Reader, admission DecodeAdmission) (Preparation, error) {
	return readPreparation(ctx, r, admission, nil)
}

// ReadPreparationWithFootprint returns the same conservative byte/structure
// budget checked during decoding. This is policy accounting, not measured RSS
// or a new admission: the caller still needs the original charged decoder.
func ReadPreparationWithFootprint(ctx context.Context, r io.Reader, admission DecodeAdmission) (Preparation, int64, error) {
	var required int64
	p, err := readPreparation(ctx, r, admission, &required)
	if err != nil {
		return Preparation{}, 0, err
	}
	return p, required, nil
}

// PreparationFootprint checks the canonical byte/structure allowance while
// the original preparation is still charged. This is a conservative policy
// check for handoff at either endpoint, not an allocation or RSS guarantee.
func PreparationFootprint(ctx context.Context, p Preparation, admission DecodeAdmission) (int64, error) {
	if ctx == nil || admission == nil {
		return 0, errors.New("original preparation admission required")
	}
	if err := admission.CheckActive(ctx); err != nil {
		return 0, err
	}
	available := admission.MemoryBytes()
	if available <= decodeReserveBytes {
		return 0, errors.New("admitted preparation memory below control overhead")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	if len(raw) == 0 || len(raw) > MaxPreparationBytes || int64(len(raw)) > (available-decodeReserveBytes)/decodeFrameCopies {
		return 0, errors.New("canonical preparation exceeds admitted memory")
	}
	var structural int64
	if err := preflightPreparationJSON(ctx, raw, available-decodeReserveBytes-decodeFrameCopies*int64(len(raw)), admission, &structural); err != nil {
		return 0, err
	}
	return decodeReserveBytes + decodeFrameCopies*int64(len(raw)) + structural, admission.CheckActive(ctx)
}

func readPreparation(ctx context.Context, r io.Reader, admission DecodeAdmission, required *int64) (Preparation, error) {
	if ctx == nil || admission == nil {
		return Preparation{}, errors.New("live decode admission required")
	}
	if err := admission.CheckActive(ctx); err != nil {
		return Preparation{}, err
	}
	available := admission.MemoryBytes()
	if available <= decodeReserveBytes {
		return Preparation{}, errors.New("admitted decode memory below control overhead")
	}
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Preparation{}, err
	}
	n := binary.BigEndian.Uint64(header[:])
	if n == 0 || n > MaxPreparationBytes {
		return Preparation{}, errors.New("sync preparation frame exceeds bound")
	}
	if n > uint64((available-decodeReserveBytes)/decodeFrameCopies) {
		return Preparation{}, errors.New("preparation exceeds admitted decode memory")
	}
	if err := admission.CheckActive(ctx); err != nil {
		return Preparation{}, err
	}
	raw := make([]byte, int(n))
	if _, err := io.ReadFull(r, raw); err != nil {
		return Preparation{}, err
	}
	if err := admission.CheckActive(ctx); err != nil {
		return Preparation{}, err
	}
	var structural int64
	if err := preflightPreparationJSON(ctx, raw, available-decodeReserveBytes-decodeFrameCopies*int64(n), admission, &structural); err != nil {
		return Preparation{}, err
	}
	var envelope struct {
		Version  int             `json:"version"`
		Intent   json.RawMessage `json:"intent"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := strictJSON(raw, &envelope); err != nil {
		return Preparation{}, err
	}
	if envelope.Version != Schema || len(envelope.Intent) == 0 || len(envelope.Intent) > MaxRecordBytes || len(envelope.Manifest) == 0 || len(envelope.Manifest) > MaxManifestBytes {
		return Preparation{}, errors.New("unsupported or oversized sync preparation")
	}
	var p Preparation
	p.Version = envelope.Version
	if err := strictJSON(envelope.Intent, &p.Intent); err != nil {
		return Preparation{}, err
	}
	if err := p.Intent.Validate(); err != nil {
		return Preparation{}, err
	}
	if sha(envelope.Manifest) != p.Intent.ManifestDigest {
		return Preparation{}, errors.New("wire manifest digest changed")
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Manifest))
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return Preparation{}, errors.New("manifest array required")
	}
	p.Manifest = make([]ManifestRow, 0)
	for decoder.More() {
		if len(p.Manifest)%1024 == 0 {
			if err = admission.CheckActive(ctx); err != nil {
				return Preparation{}, err
			}
		}
		if len(p.Manifest) >= maxManifestRows {
			return Preparation{}, errors.New("decoded manifest row limit exceeded")
		}
		var row ManifestRow
		if err = decoder.Decode(&row); err != nil {
			return Preparation{}, err
		}
		p.Manifest = append(p.Manifest, row)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return Preparation{}, errors.New("unterminated manifest")
	}
	if _, err = decoder.Token(); err != io.EOF {
		if err == nil {
			return Preparation{}, errors.New("trailing manifest data")
		}
		return Preparation{}, err
	}
	if err = validateManifest(p.Intent, p.Manifest); err != nil {
		return Preparation{}, err
	}
	if err = validateFootprint(p.Intent, len(envelope.Manifest), len(envelope.Intent)); err != nil {
		return Preparation{}, err
	}
	if err = admission.CheckActive(ctx); err != nil {
		return Preparation{}, err
	}
	if required != nil {
		*required = decodeReserveBytes + decodeFrameCopies*int64(n) + structural
	}
	return p, nil
}

const maxManifestRows = 6 * 200000 // bounded by both endpoint native-name maps

type preparationPath uint8

// Only schema locations that carry collection limits have identities here.
// Unknown keys never become ancestry strings: a long key under deep JSON
// nesting must not be copied once per ancestor by the preflight itself.
const (
	pathOther preparationPath = iota
	pathRoot
	pathIntent
	pathProposal
	pathBinding
	pathProposalBinding
	pathProposalLocal
	pathProposalRemote
	pathActions
	pathResolutions
	pathExclusions
	pathManifest
)

func preparationChildPath(parent preparationPath, key string) preparationPath {
	switch parent {
	case pathRoot:
		if strings.EqualFold(key, "intent") {
			return pathIntent
		}
		if strings.EqualFold(key, "manifest") {
			return pathManifest
		}
	case pathIntent:
		switch {
		case strings.EqualFold(key, "proposal"):
			return pathProposal
		case strings.EqualFold(key, "binding"):
			return pathBinding
		case strings.EqualFold(key, "actions"):
			return pathActions
		case strings.EqualFold(key, "resolutions"):
			return pathResolutions
		}
	case pathProposal:
		switch {
		case strings.EqualFold(key, "local"):
			return pathProposalLocal
		case strings.EqualFold(key, "remote"):
			return pathProposalRemote
		case strings.EqualFold(key, "binding"):
			return pathProposalBinding
		}
	case pathBinding, pathProposalBinding:
		if strings.EqualFold(key, "exclusions") {
			return pathExclusions
		}
	}
	return pathOther
}

// encoding/json.Marshal uses HTML escaping and keeps an encoder buffer while
// returning a copy. Count its worst escaped string bytes without constructing
// another string; each output byte is charged for buffer growth and the copy.
func canonicalStringBytes(s string) int64 {
	var n int64
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '<' || c == '>' || c == '&' || c < 0x20:
			n += 6
			i++
		case c == '\\' || c == '"':
			n += 2
			i++
		case c < utf8.RuneSelf:
			n++
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError || r == '\u2028' || r == '\u2029' {
				n += 6
			} else {
				n += int64(size)
			}
			i += size
		}
	}
	return n + 2 // surrounding JSON quotes
}

// The standard typed decoder grows maps and slices from JSON member counts,
// not wire bytes. A sparse state such as "x":{} can occupy far more heap than
// its encoding. This pass retains no decoded collection and rejects before
// unmarshalling Intent or Manifest. Charges cover map buckets, struct values,
// slice growth, decoded strings, and concurrent decoder/marshal copies.
func preflightPreparationJSON(ctx context.Context, raw []byte, remaining int64, admission DecodeAdmission, used *int64) error {
	budget := remaining
	defer func() {
		if used != nil {
			*used = budget - remaining
		}
	}()
	if remaining < 0 {
		return errors.New("preparation exceeds admitted decode memory")
	}
	type frame struct {
		kind          json.Delim
		path, pending preparationPath
		count         int
		expectKey     bool
	}
	stack := make([]frame, 0, 12)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	rootSeen := false
	var tokens int
	charge := func(n int64) error {
		if n < 0 || n > remaining {
			return errors.New("preparation exceeds admitted structural decode memory")
		}
		remaining -= n
		return nil
	}
	beginValue := func() (preparationPath, error) {
		if len(stack) == 0 {
			if rootSeen {
				return pathOther, errors.New("multiple preparation JSON values")
			}
			rootSeen = true
			return pathRoot, nil
		}
		parent := &stack[len(stack)-1]
		if parent.kind == '{' {
			if parent.expectKey {
				return pathOther, errors.New("preparation JSON object lacks key")
			}
			parent.expectKey = true
			child := parent.pending
			parent.pending = pathOther
			return child, nil
		}
		parent.count++
		if err := charge(decodeArrayElementBytes); err != nil {
			return pathOther, err
		}
		if parent.count > collectionLimit(parent.path, '[') {
			return pathOther, errors.New("preparation JSON array exceeds count limit")
		}
		return pathOther, nil
	}
	for {
		if tokens%1024 == 0 {
			if err := admission.CheckActive(ctx); err != nil {
				return err
			}
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		tokens++
		switch v := token.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				path, err := beginValue()
				if err != nil {
					return err
				}
				if len(stack) >= maxJSONDepth {
					return errors.New("preparation JSON nesting exceeds limit")
				}
				if err = charge(decodeContainerBytes); err != nil {
					return err
				}
				stack = append(stack, frame{kind: v, path: path, expectKey: v == '{'})
			case '}', ']':
				if len(stack) == 0 || stack[len(stack)-1].kind == '{' && v != '}' || stack[len(stack)-1].kind == '[' && v != ']' {
					return errors.New("preparation JSON delimiter mismatch")
				}
				stack = stack[:len(stack)-1]
			}
		case string:
			if len(stack) > 0 && stack[len(stack)-1].kind == '{' && stack[len(stack)-1].expectKey {
				parent := &stack[len(stack)-1]
				parent.count++
				if err = charge(decodeObjectMemberBytes + 4*int64(len(v)) + decodeCanonicalCopies*canonicalStringBytes(v)); err != nil {
					return err
				}
				if parent.count > collectionLimit(parent.path, '{') {
					return errors.New("preparation JSON object exceeds count limit")
				}
				parent.pending = preparationChildPath(parent.path, v)
				parent.expectKey = false
			} else {
				if _, err = beginValue(); err != nil {
					return err
				}
				if err = charge(4*int64(len(v)) + 32 + decodeCanonicalCopies*canonicalStringBytes(v)); err != nil {
					return err
				}
			}
		default:
			if _, err = beginValue(); err != nil {
				return err
			}
		}
	}
	if !rootSeen || len(stack) != 0 {
		return errors.New("incomplete preparation JSON")
	}
	return nil
}

func collectionLimit(path preparationPath, kind json.Delim) int {
	switch path {
	case pathProposalLocal, pathProposalRemote:
		if kind == '{' {
			return syncreconcile.MaxEntries
		}
	case pathActions:
		if kind == '[' {
			return syncreconcile.MaxActions
		}
	case pathResolutions:
		if kind == '[' {
			return syncreconcile.MaxResolutions
		}
	case pathExclusions:
		if kind == '[' {
			return syncreconcile.MaxEntries
		}
	case pathManifest:
		if kind == '[' {
			return maxManifestRows
		}
	}
	return maxManifestRows
}

func strictJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}
