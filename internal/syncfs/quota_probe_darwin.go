package syncfs

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const maxDiskutilOutput = 2 << 20

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxDiskutilOutput-b.Len() {
		return 0, ErrQuotaUnknown
	}
	return b.Buffer.Write(p)
}

func diskutilPlist(ctx context.Context, args ...string) (map[string]any, error) {
	return diskutilPlistCommand(ctx, "/usr/sbin/diskutil", args...)
}

// WaitDelay bounds the pipe-copy wait if a descendant of diskutil inherits
// stdout after the original command exits. This does not join or reclaim that
// descendant; an incomplete query is unknown evidence.
func diskutilPlistCommand(ctx context.Context, executable string, args ...string) (map[string]any, error) {
	return diskutilPlistCommandWithFiles(ctx, nil, executable, args...)
}

// extraFiles is a test-only descriptor loan used to prove the pipe wait is
// bounded without creating an unowned descendant. Production always passes nil.
func diskutilPlistCommandWithFiles(ctx context.Context, extraFiles []*os.File, executable string, args ...string) (map[string]any, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(queryCtx, executable, args...)
	cmd.ExtraFiles = extraFiles
	cmd.WaitDelay = 250 * time.Millisecond
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || queryCtx.Err() != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrQuotaUnknown
	}
	return decodePlistDictionary(output.Bytes())
}

func decodePlistDictionary(data []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// diskutil emits an XML declaration and may emit a plist DOCTYPE. No
	// substantive content may precede the single plist root.
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrQuotaUnknown
		}
		switch v := token.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(v)) != 0 {
				return nil, ErrQuotaUnknown
			}
			continue
		case xml.ProcInst:
			if v.Target != "xml" {
				return nil, ErrQuotaUnknown
			}
			continue
		case xml.Directive:
			if !bytes.HasPrefix(bytes.TrimSpace(v), []byte("DOCTYPE plist ")) {
				return nil, ErrQuotaUnknown
			}
			continue
		case xml.StartElement:
			if v.Name.Local != "plist" || v.Name.Space != "" || len(v.Attr) != 1 ||
				v.Attr[0].Name.Local != "version" || v.Attr[0].Name.Space != "" || v.Attr[0].Value != "1.0" {
				return nil, ErrQuotaUnknown
			}
		default:
			return nil, ErrQuotaUnknown
		}
		for {
			token, err = decoder.Token()
			if err != nil {
				return nil, ErrQuotaUnknown
			}
			if whitespace, ok := token.(xml.CharData); ok && len(bytes.TrimSpace(whitespace)) == 0 {
				continue
			}
			if value, ok := token.(xml.StartElement); ok {
				result, err := decodePlistValue(decoder, value, 0)
				if err != nil {
					return nil, err
				}
				dict, ok := result.(map[string]any)
				if !ok {
					return nil, ErrQuotaUnknown
				}
				for {
					closing, err := decoder.Token()
					if err != nil {
						return nil, ErrQuotaUnknown
					}
					if whitespace, ok := closing.(xml.CharData); ok && len(bytes.TrimSpace(whitespace)) == 0 {
						continue
					}
					end, ok := closing.(xml.EndElement)
					if !ok || end.Name.Local != "plist" {
						return nil, ErrQuotaUnknown
					}
					break
				}
				for {
					trailing, err := decoder.Token()
					if err == io.EOF {
						break
					}
					if err != nil {
						return nil, ErrQuotaUnknown
					}
					if whitespace, ok := trailing.(xml.CharData); !ok || len(bytes.TrimSpace(whitespace)) != 0 {
						return nil, ErrQuotaUnknown
					}
				}
				return dict, nil
			}
			return nil, ErrQuotaUnknown
		}
	}
}

func decodePlistValue(decoder *xml.Decoder, start xml.StartElement, depth int) (any, error) {
	if depth > 16 || start.Name.Space != "" || len(start.Attr) != 0 {
		return nil, ErrQuotaUnknown
	}
	switch start.Name.Local {
	case "dict":
		out := map[string]any{}
		key := ""
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, ErrQuotaUnknown
			}
			switch v := token.(type) {
			case xml.CharData:
				if len(bytes.TrimSpace(v)) != 0 {
					return nil, ErrQuotaUnknown
				}
			case xml.EndElement:
				if v.Name.Local == "dict" && v.Name.Space == "" && key == "" {
					return out, nil
				}
				return nil, ErrQuotaUnknown
			case xml.StartElement:
				value, err := decodePlistValue(decoder, v, depth+1)
				if err != nil {
					return nil, err
				}
				if v.Name.Local == "key" {
					name, ok := value.(string)
					if !ok || name == "" || key != "" || len(out) >= 256 {
						return nil, ErrQuotaUnknown
					}
					key = name
				} else {
					if key == "" {
						return nil, ErrQuotaUnknown
					}
					if _, duplicate := out[key]; duplicate {
						return nil, ErrQuotaUnknown
					}
					out[key], key = value, ""
				}
			default:
				return nil, ErrQuotaUnknown
			}
		}
	case "array":
		var out []any
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, ErrQuotaUnknown
			}
			switch v := token.(type) {
			case xml.CharData:
				if len(bytes.TrimSpace(v)) != 0 {
					return nil, ErrQuotaUnknown
				}
			case xml.EndElement:
				if v.Name.Local == "array" && v.Name.Space == "" {
					return out, nil
				}
				return nil, ErrQuotaUnknown
			case xml.StartElement:
				if len(out) >= 256 {
					return nil, ErrQuotaUnknown
				}
				value, err := decodePlistValue(decoder, v, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			default:
				return nil, ErrQuotaUnknown
			}
		}
	case "string", "key", "integer":
		var value strings.Builder
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, ErrQuotaUnknown
			}
			switch v := token.(type) {
			case xml.CharData:
				value.Write(v)
			case xml.EndElement:
				if v.Name != start.Name {
					return nil, ErrQuotaUnknown
				}
				if start.Name.Local == "integer" {
					n, err := strconv.ParseUint(strings.TrimSpace(value.String()), 10, 64)
					if err != nil {
						return nil, ErrQuotaUnknown
					}
					return n, nil
				}
				return value.String(), nil
			default:
				return nil, ErrQuotaUnknown
			}
		}
	case "true", "false":
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrQuotaUnknown
		}
		end, ok := token.(xml.EndElement)
		if !ok || end.Name != start.Name {
			return nil, ErrQuotaUnknown
		}
		return start.Name.Local == "true", nil
	default:
		return nil, ErrQuotaUnknown
	}
	return nil, ErrQuotaUnknown
}

func apfsQuotaFromPlists(info, list map[string]any, mount string) (NativeQuotaEvidence, error) {
	filesystem, ok := info["FilesystemName"].(string)
	if !ok || filesystem == "" {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if filesystem != "APFS" {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	reportedMount, ok := info["MountPoint"].(string)
	if !ok || reportedMount == "" {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if reportedMount != mount {
		return NativeQuotaEvidence{}, ErrChanged
	}
	writable, ok := info["WritableVolume"].(bool)
	if !ok {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if !writable {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	device, ok := info["DeviceIdentifier"].(string)
	if !ok || device == "" {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	uuid, ok := info["VolumeUUID"].(string)
	if !ok || uuid == "" {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	containers, ok := list["Containers"].([]any)
	if !ok {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	var match map[string]any
	for _, c := range containers {
		container, ok := c.(map[string]any)
		if !ok {
			return NativeQuotaEvidence{}, ErrQuotaUnknown
		}
		volumes, ok := container["Volumes"].([]any)
		if !ok {
			return NativeQuotaEvidence{}, ErrQuotaUnknown
		}
		for _, v := range volumes {
			volume, ok := v.(map[string]any)
			if !ok {
				return NativeQuotaEvidence{}, ErrQuotaUnknown
			}
			if volume["DeviceIdentifier"] == device {
				if match != nil {
					return NativeQuotaEvidence{}, ErrQuotaUnknown
				}
				match = volume
			}
		}
	}
	if match == nil {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	listedUUID, ok := match["APFSVolumeUUID"].(string)
	if !ok || listedUUID == "" {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if !strings.EqualFold(listedUUID, uuid) {
		return NativeQuotaEvidence{}, ErrChanged
	}
	hard, okHard := match["CapacityQuota"].(uint64)
	used, okUsed := match["CapacityInUse"].(uint64)
	if !okHard || !okUsed || used > hard && hard != 0 {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if hard == 0 {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	return NativeQuotaEvidence{provider: "apfs-volume-quota", scope: "apfs-volume:" + uuid, hardBytes: hard, usedBytes: used}, nil
}

func probePlatformQuota(ctx context.Context, selected, parent *Root, mount string) (NativeQuotaEvidence, error) {
	var selectedFS, parentFS unix.Statfs_t
	if err := unix.Fstatfs(selected.FD(), &selectedFS); err != nil {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if err := unix.Fstatfs(parent.FD(), &parentFS); err != nil {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if unix.ByteSliceToString(selectedFS.Fstypename[:]) != "apfs" ||
		unix.ByteSliceToString(parentFS.Fstypename[:]) != "apfs" {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	mountpoint := unix.ByteSliceToString(selectedFS.Mntonname[:])
	if mountpoint == "" || mountpoint != unix.ByteSliceToString(parentFS.Mntonname[:]) {
		return NativeQuotaEvidence{}, ErrChanged
	}
	info, err := diskutilPlist(ctx, "info", "-plist", mountpoint)
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	list, err := diskutilPlist(ctx, "apfs", "list", "-plist")
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	v, err := apfsQuotaFromPlists(info, list, mountpoint)
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	observedMount, err := surveyDarwinMountIdentity(selectedFS)
	if err != nil || mount != observedMount {
		return NativeQuotaEvidence{}, ErrChanged
	}
	return v, nil
}
