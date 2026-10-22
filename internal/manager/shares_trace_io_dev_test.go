//go:build arm_devtrace

package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Dev inputs/outputs are restricted to the ignored artifact namespace, too.
func formalPath(path string) (string, error) {
	root, err := filepath.Abs("../../.tla")
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("formal artifact root must be a directory, not a symlink")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("formal path must stay under .tla")
	}
	current := root
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		st, e := os.Lstat(current)
		if e != nil {
			return "", e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("formal path has non-directory/symlink")
		}
	}
	return path, nil
}
func formalReadJSON(path string, out any) error {
	path, err := formalPath(path)
	if err != nil {
		return err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("regular formal input required")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("formal input exceeds1MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := formalJSONValue(dec)
	if err != nil {
		return err
	}
	if _, err = dec.Token(); err != io.EOF {
		return fmt.Errorf("trailing formal JSON")
	}
	if err = formalClosedShape(value, reflect.TypeOf(out).Elem()); err != nil {
		return err
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
func formalJSONValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid key")
			}
			if _, ok = m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON key")
			}
			v, e := formalJSONValue(d)
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, err = d.Token()
		return m, err
	case json.Delim('['):
		var v []any
		for d.More() {
			x, e := formalJSONValue(d)
			if e != nil {
				return nil, e
			}
			v = append(v, x)
		}
		_, err = d.Token()
		return v, err
	default:
		return token, nil
	}
}
func formalClosedShape(v any, t reflect.Type) error {
	if v == nil {
		return fmt.Errorf("null formal evidence")
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok || len(m) != t.NumField() {
			return fmt.Errorf("closed complete formal record required")
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			x, ok := m[name]
			if !ok {
				return fmt.Errorf("missing/noncanonical formal field %s", name)
			}
			if err := formalClosedShape(x, f.Type); err != nil {
				return err
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok || len(a) > 256 {
			return fmt.Errorf("bounded formal array required")
		}
		for _, x := range a {
			if err := formalClosedShape(x, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
func formalWriteJSON(path string, v any) error {
	path, err := formalPath(path)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("formal output exceeds1MiB")
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}
