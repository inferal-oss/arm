// Package daemonconfig resolves desired local startup settings without creating
// state or contacting a daemon. It does not administer owner policy budgets.
package daemonconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
	"github.com/pelletier/go-toml/v2"
)

const Filename = "config.toml"
const MaxBytes = 64 << 10

// Settings contains configured ceilings, never ephemeral observed headroom.
// Zero memory/disk means auto, not zero allowance in an owner policy budget.
type Settings struct {
	StoragePools []model.StoragePoolConfig `json:"storage_pools,omitempty"`
	Resources    model.Resources           `json:"resources"`
	Listen       string                    `json:"listen"`
}

func (s Settings) Validate() error {
	if len(s.StoragePools) > 8 {
		return fmt.Errorf("at most eight storage pools may be configured")
	}
	seen := map[string]bool{}
	for _, p := range s.StoragePools {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`).MatchString(p.Name) || p.Name == "default" || seen[p.Name] {
			return fmt.Errorf("storage pool names must be unique lowercase identifiers, excluding default")
		}
		seen[p.Name] = true
		if !filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path || len(p.Path) > 512 || len(p.Tier) > 64 || strings.ContainsAny(p.Path+p.Tier, "\x00\n\r") || p.Ceiling < 0 {
			return fmt.Errorf("storage pool requires a clean absolute path up to 512 bytes, bounded tier and nonnegative ceiling")
		}
	}

	if math.IsNaN(s.Resources.CPU) || math.IsInf(s.Resources.CPU, 0) || s.Resources.CPU <= 0 || s.Resources.CPU > 65536 || s.Resources.Memory < 0 || s.Resources.Disk < 0 {
		return fmt.Errorf("CPU must be finite and positive, at most 65536; memory/disk must be auto or positive byte ceilings")
	}
	host, port, err := net.SplitHostPort(s.Listen)
	ip := net.ParseIP(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 0 || n > 65535 || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen must be a literal loopback IP and port 0..65535 (for example 127.0.0.1:0)")
	}
	return nil
}

type Overrides struct {
	CPU                  *float64
	Memory, Disk, Listen *string
}

type Resolved struct {
	Path           string            `json:"path"`
	Settings       Settings          `json:"settings"`
	Sources        map[string]string `json:"sources"`
	FilePresent    bool              `json:"file_present"`
	FileSHA256     string            `json:"file_sha256,omitempty"`
	SettingsSHA256 string            `json:"settings_sha256"`
	Scope          string            `json:"scope"`
	Unsupported    []string          `json:"unsupported"`
}

// SnapshotSHA256 binds the complete validated resolved values, independent of
// spelling, provenance or mutable file metadata. It grants no authority.
func SnapshotSHA256(settings Settings) (string, error) {
	if err := settings.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type storagePoolDocument struct {
	Name    string `toml:"name"`
	Path    string `toml:"path"`
	Tier    string `toml:"tier"`
	Ceiling string `toml:"ceiling"`
}

type document struct {
	StoragePools []storagePoolDocument `toml:"storage_pools"`
	Schema       int                   `toml:"schema"`
	Daemon       struct {
		CPU    *float64 `toml:"cpu"`
		Memory *string  `toml:"memory"`
		Disk   *string  `toml:"disk"`
		Listen *string  `toml:"listen"`
	} `toml:"daemon"`
}

// Ceiling parses exact whole bytes. Fractional units are allowed only if they
// resolve to an integral positive byte count; there is no float rounding.
func Ceiling(raw string) (int64, error) {
	if raw == "auto" {
		return 0, nil
	}
	s := strings.TrimSpace(raw)
	mult := int64(1)
	for _, u := range []struct {
		name string
		n    int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1000000000000}, {"GB", 1000000000}, {"MB", 1000000}, {"KB", 1000}, {"B", 1}} {
		if strings.HasSuffix(s, u.name) {
			s = strings.TrimSuffix(s, u.name)
			mult = u.n
			break
		}
	}
	// Bound decimal input and reject rational/exponent/hex syntax.
	if len(s) == 0 || len(s) > 32 || strings.Count(s, ".") > 1 || strings.Trim(s, "0123456789.") != "" {
		return 0, fmt.Errorf("expected auto or a positive whole-byte size such as 512MiB")
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("invalid byte ceiling")
	}
	n.Mul(n, new(big.Rat).SetInt64(mult))
	if !n.IsInt() || !n.Num().IsInt64() || n.Sign() <= 0 {
		return 0, fmt.Errorf("ceiling must resolve to positive whole bytes within int64")
	}
	return n.Num().Int64(), nil
}

func apply(s *Settings, source map[string]string, o Overrides, origin string) error {
	if o.CPU != nil {
		s.Resources.CPU = *o.CPU
		source["cpu"] = origin
	}
	for _, f := range []struct {
		name   string
		raw    *string
		target *int64
	}{{"memory", o.Memory, &s.Resources.Memory}, {"disk", o.Disk, &s.Resources.Disk}} {
		if f.raw != nil {
			n, err := Ceiling(*f.raw)
			if err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
			*f.target = n
			source[f.name] = origin
		}
	}
	if o.Listen != nil {
		s.Listen = *o.Listen
		source["listen"] = origin
	}
	return s.Validate()
}

// Resolve validates the entire file before considering overrides: an explicit
// flag cannot hide a broken/unknown setting. A missing file means defaults.
func Resolve(home string, defaults Settings, flags Overrides) (Resolved, error) {
	absolute, _ := filepath.Abs(filepath.Join(home, Filename))
	r := Resolved{Path: absolute, Settings: defaults, Sources: map[string]string{"cpu": "default", "memory": "default", "disk": "default", "listen": "default"}, Scope: "Desired next ordinary local startup only; not a running-daemon read or a live apply.", Unsupported: []string{"owner/nested policy budgets: use arm budget", "per-lease usage/review policy: use the existing lease commands", "reserved human/OS headroom, provider configuration, profiles, service/watchdog, retention, restore and messaging defaults"}}
	if err := defaults.Validate(); err != nil {
		return r, err
	}
	d, err := database.OpenDirectory(home)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	} else if err == nil {
		defer d.Close()
		f, info, e := d.Open(Filename, os.O_RDONLY)
		if e != nil {
			return r, fmt.Errorf("%s: unsafe or unreadable file: %w", Filename, e)
		}
		if f != nil {
			defer f.Close()
			if info.Size > MaxBytes {
				return r, fmt.Errorf("%s exceeds 64 KiB", Filename)
			}
			raw, e := io.ReadAll(io.LimitReader(f, MaxBytes+1))
			if e != nil {
				return r, e
			}
			if len(raw) > MaxBytes {
				return r, fmt.Errorf("%s exceeds 64 KiB", Filename)
			}
			if e = d.Validate(); e != nil {
				return r, e
			}
			if e = applyDocument(raw, &r); e != nil {
				return r, e
			}
			r.FilePresent = true
			sum := sha256.Sum256(raw)
			r.FileSHA256 = hex.EncodeToString(sum[:])
		}
	}
	if err != nil {
		return r, fmt.Errorf("read %s in the selected private ARM home: %w", Filename, err)
	}
	if err = apply(&r.Settings, r.Sources, flags, "explicit-startup-flag"); err != nil {
		return r, fmt.Errorf("startup flags: %w", err)
	}
	r.SettingsSHA256, err = SnapshotSHA256(r.Settings)
	if err != nil {
		return r, err
	}
	return r, nil
}

func applyDocument(raw []byte, r *Resolved) error {
	var e error
	var doc document
	if e = toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields().Decode(&doc); e != nil {
		return fmt.Errorf("%s: invalid configuration; check supported keys with arm config show: %w", Filename, e)
	}
	if doc.Schema != 1 {
		return fmt.Errorf("%s: schema = 1 is required; unsupported configuration is not applied", Filename)
	}
	for _, p := range doc.StoragePools {
		ceiling := int64(0)
		if p.Ceiling != "" {
			ceiling, e = Ceiling(p.Ceiling)
			if e != nil {
				return fmt.Errorf("storage pool %s: %w", p.Name, e)
			}
		}
		r.Settings.StoragePools = append(r.Settings.StoragePools, model.StoragePoolConfig{Name: p.Name, Path: p.Path, Tier: p.Tier, Ceiling: ceiling})
	}
	if len(doc.StoragePools) > 0 {
		r.Sources["storage_pools"] = "file"
	}
	if e = apply(&r.Settings, r.Sources, Overrides{doc.Daemon.CPU, doc.Daemon.Memory, doc.Daemon.Disk, doc.Daemon.Listen}, "file"); e != nil {
		return fmt.Errorf("%s: %w", Filename, e)
	}
	return nil
}
