package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

type preferences struct {
	Version      int  `json:"version"`
	HideInactive bool `json:"hide_inactive_leases"`
}

const preferenceFile = "dashboard-client.json"

func trustedClientRoot(home string) (*os.Root, error) {
	info, err := os.Lstat(home)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("client settings require a private real state directory")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("client settings directory owner differs")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("client settings directory changed while opening")
	}
	return root, nil
}
func privatePreference(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && info.Size() <= 4096
}
func readPreferences(home string) (preferences, error) {
	p := preferences{Version: 1}
	root, err := trustedClientRoot(home)
	if err != nil {
		return p, err
	}
	defer root.Close()
	f, err := root.OpenFile(preferenceFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return p, err
	}
	if !privatePreference(info) {
		return p, fmt.Errorf("client settings must be a private regular file of at most 4096 bytes")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 4097))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&p); err != nil {
		return preferences{Version: 1}, err
	}
	if p.Version != 1 {
		return preferences{Version: 1}, fmt.Errorf("unknown client settings version")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return preferences{Version: 1}, fmt.Errorf("trailing client settings data")
	}
	return p, nil
}
func writePreferences(home string, p preferences) error {
	root, err := trustedClientRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, e := root.Lstat(preferenceFile); e == nil && !privatePreference(info) {
		return fmt.Errorf("refusing to replace unsafe client settings file")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	name := ".dashboard-client-" + local.ID()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	err = json.NewEncoder(f).Encode(p)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return root.Rename(name, preferenceFile)
}
func leaseLabel(lease model.Lease) string {
	if lease.State != "terminal" {
		return human(lease.State)
	}
	switch lease.Outcome {
	case "released":
		return "Released"
	case "cancelled":
		return "Cancelled"
	case "expired":
		return "Expired"
	case "revoked":
		return "Revoked"
	case "completed", "succeeded", "success":
		return "Completed"
	case "failed", "launch_failed", "admission_failed":
		return "Failed"
	case "interrupted":
		return "Interrupted"
	case "reconciled":
		return "Reconciled"
	case "handed_off":
		return "Handed off"
	case "":
		return "Inactive"
	}
	return human(lease.Outcome)
}
func human(value string) string {
	s := []rune(safe(value))
	if len(s) == 0 {
		return "Unknown"
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		s[0] -= 'a' - 'A'
	}
	for i, c := range s {
		if c == '_' {
			s[i] = ' '
		}
	}
	return string(s)
}
