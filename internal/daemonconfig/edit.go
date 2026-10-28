package daemonconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"getarm.org/internal/database"
	"getarm.org/internal/local"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
	"golang.org/x/sys/unix"
)

type Edit struct {
	Key        string
	Value      string
	Pool       string
	Remove     bool
	Fields     map[string]string
	ExpectFile string
	DryRun     bool
}
type EditResult struct {
	Path         string `json:"path"`
	Written      bool   `json:"written"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	TOML         string `json:"toml"`
	Scope        string `json:"scope"`
}
type scalarRange struct {
	key        string
	start, end int
}
type configSection struct {
	name       string
	start, end int
	values     []scalarRange
}

// sourceSections records scalar source ranges so editing one supported field
// preserves all unrelated values, comments and spelling. Unusual inline-table
// forms remain readable but refuse edits that cannot preserve their structure.
func sourceSections(raw []byte) ([]configSection, error) {
	sections := []configSection{{end: len(raw)}}
	var parser unstable.Parser
	parser.Reset(raw)
	lineStart := func(pos int) int { return bytes.LastIndexByte(raw[:pos], '\n') + 1 }
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind != unstable.Table && n.Kind != unstable.ArrayTable && n.Kind != unstable.KeyValue {
			continue
		}
		keys := []string{}
		it := n.Key()
		keyStart := 0
		for it.Next() {
			k := it.Node()
			if len(keys) == 0 {
				keyStart = int(k.Raw.Offset)
			}
			keys = append(keys, string(k.Data))
		}
		key := strings.Join(keys, ".")
		if n.Kind != unstable.KeyValue {
			start := lineStart(keyStart)
			sections[len(sections)-1].end = start
			sections = append(sections, configSection{name: key, start: start, end: len(raw)})
			continue
		}
		v := n.Value()
		sections[len(sections)-1].values = append(sections[len(sections)-1].values, scalarRange{key, int(v.Raw.Offset), int(v.Raw.Offset + v.Raw.Length)})
	}
	return sections, parser.Error()
}
func quoted(value string) string { return strconv.Quote(value) }
func editDocument(raw []byte, edit Edit) ([]byte, error) {
	sections, err := sourceSections(raw)
	if err != nil {
		return nil, err
	}
	patch := func(start, end int, value string) []byte {
		out := append([]byte{}, raw[:start]...)
		out = append(out, value...)
		return append(out, raw[end:]...)
	}
	if edit.Pool == "" {
		key := strings.TrimPrefix(edit.Key, "daemon.")
		if edit.Key != "daemon."+key || !strings.Contains("|cpu|memory|disk|listen|", "|"+key+"|") {
			return nil, fmt.Errorf("supported keys: daemon.cpu, daemon.memory, daemon.disk, daemon.listen")
		}
		value := quoted(edit.Value)
		if key == "cpu" {
			number, err := strconv.ParseFloat(edit.Value, 64)
			if err != nil {
				return nil, fmt.Errorf("daemon.cpu requires one numeric value")
			}
			value = strconv.FormatFloat(number, 'g', -1, 64)
		}
		for _, s := range sections {
			for _, r := range s.values {
				if s.name == "daemon" && r.key == key || s.name == "" && r.key == edit.Key {
					return patch(r.start, r.end, value), nil
				}
			}
		}
		for _, s := range sections {
			if s.name == "daemon" {
				return patch(s.end, s.end, "\n"+key+" = "+value+"\n"), nil
			}
		}
		return patch(sections[0].end, sections[0].end, "\n"+edit.Key+" = "+value+"\n"), nil
	}
	var doc document
	if err = toml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	index := -1
	for i, p := range doc.StoragePools {
		if p.Name == edit.Pool {
			index = i
		}
	}
	poolSections := []configSection{}
	for _, s := range sections {
		if s.name == "storage_pools" {
			poolSections = append(poolSections, s)
		}
	}
	if len(poolSections) != len(doc.StoragePools) {
		return nil, fmt.Errorf("pool commands require [[storage_pools]] sections; inline forms must be edited manually")
	}
	if edit.Remove {
		if index < 0 {
			return nil, fmt.Errorf("storage pool not found")
		}
		s := poolSections[index]
		type span struct{ start, end int }
		spans := []span{}
		lineEnd := func(pos int) int {
			if end := bytes.IndexByte(raw[pos:], '\n'); end >= 0 {
				return pos + end + 1
			}
			return len(raw)
		}
		spans = append(spans, span{s.start, lineEnd(s.start)})
		for _, v := range s.values {
			spans = append(spans, span{bytes.LastIndexByte(raw[:v.start], '\n') + 1, lineEnd(v.end)})
		}
		for i := len(spans) - 1; i >= 0; i-- {
			r := spans[i]
			raw = patch(r.start, r.end, "")
		}
		return raw, nil
	}
	if index < 0 {
		var b strings.Builder
		b.WriteString("\n[[storage_pools]]\nname = " + quoted(edit.Pool) + "\n")
		for _, key := range []string{"path", "tier", "ceiling"} {
			if v, ok := edit.Fields[key]; ok {
				b.WriteString(key + " = " + quoted(v) + "\n")
			}
		}
		return append(append([]byte{}, raw...), []byte(b.String())...), nil
	}
	// Reparse after each scalar edit: offsets always refer to the current text.
	for _, key := range []string{"path", "tier", "ceiling"} {
		value, ok := edit.Fields[key]
		if !ok {
			continue
		}
		sections, err = sourceSections(raw)
		if err != nil {
			return nil, err
		}
		count := 0
		for _, s := range sections {
			if s.name != "storage_pools" {
				continue
			}
			if count != index {
				count++
				continue
			}
			found := false
			for _, r := range s.values {
				if r.key == key {
					raw = patch(r.start, r.end, quoted(value))
					found = true
					break
				}
			}
			if !found {
				raw = patch(s.end, s.end, "\n"+key+" = "+quoted(value)+"\n")
			}
			break
		}
	}
	return raw, nil
}

// EditFile writes no backend state. A directory flock serializes ARM editors;
// a final identity/content comparison also refuses concurrent external edits.
// Publication uses exclusive private staging, fsync, renameat and directory sync.
func EditFile(home string, defaults Settings, edit Edit) (result EditResult, err error) {
	if !edit.DryRun {
		if err = local.Prepare(home); err != nil {
			return result, err
		}
	}
	d, err := database.OpenDirectory(home)
	if err != nil {
		return result, err
	}
	defer d.Close()
	if err = unix.Flock(int(d.FD.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return result, fmt.Errorf("another config edit is in progress: %w", err)
	}
	defer unix.Flock(int(d.FD.Fd()), unix.LOCK_UN)
	read := func() ([]byte, database.File, error) {
		f, info, e := d.Open(Filename, os.O_RDONLY)
		if e != nil || f == nil {
			return nil, info, e
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, MaxBytes+1))
		if len(data) > MaxBytes {
			return nil, info, fmt.Errorf("configuration exceeds 64 KiB")
		}
		return data, info, e
	}
	before, identity, err := read()
	if err != nil {
		return result, err
	}
	digest := func(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
	result.Path = d.Path + string(os.PathSeparator) + Filename
	result.BeforeSHA256 = digest(before)
	result.Scope = "Saved configuration for next ordinary daemon startup; running daemon unchanged"
	if edit.ExpectFile != "" && edit.ExpectFile != result.BeforeSHA256 {
		return result, fmt.Errorf("configuration changed; inspect config show before retrying")
	}
	raw := before
	if !identity.Present {
		raw = []byte("schema = 1\n")
	}
	validate := func(raw []byte) error {
		r := Resolved{Settings: defaults, Sources: map[string]string{}}
		if e := applyDocument(raw, &r); e != nil {
			return e
		}
		return r.Settings.Validate()
	}
	if err = validate(raw); err != nil {
		return result, fmt.Errorf("existing configuration invalid; no edit applied: %w", err)
	}
	after, err := editDocument(raw, edit)
	if err != nil {
		return result, err
	}
	if len(after) > MaxBytes {
		return result, fmt.Errorf("edited configuration exceeds 64 KiB")
	}
	if err = validate(after); err != nil {
		return result, fmt.Errorf("proposed configuration invalid; no edit applied: %w", err)
	}
	result.AfterSHA256 = digest(after)
	result.TOML = string(after)
	if edit.DryRun || bytes.Equal(before, after) {
		return result, nil
	}
	temp := ".config-" + local.ID() + ".tmp"
	fd, err := unix.Openat(int(d.FD.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return result, err
	}
	f := os.NewFile(uintptr(fd), temp)
	defer unix.Unlinkat(int(d.FD.Fd()), temp, 0)
	_, err = f.Write(after)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return result, err
	}
	current, now, err := read()
	if err != nil {
		return result, err
	}
	if !bytes.Equal(before, current) || identity.Present != now.Present || identity.Inode != now.Inode || identity.Device != now.Device {
		return result, fmt.Errorf("configuration changed during edit; no replacement applied")
	}
	if err = d.Validate(); err != nil {
		return result, err
	}
	if err = unix.Renameat(int(d.FD.Fd()), temp, int(d.FD.Fd()), Filename); err != nil {
		return result, err
	}
	result.Written = true
	if err = d.Sync(); err != nil {
		return result, fmt.Errorf("configuration replaced but directory sync unconfirmed; inspect before retry: %w", err)
	}
	return result, nil
}
