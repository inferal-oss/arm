package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// publishReady publishes one closed, fsynced private file without adopting or
// replacing any final path. beforePublish is nil in run; private tests use it
// only to pause after successful native Close and before the final Link.
func publishReady(path string, raw []byte, beforePublish func(string) error) (err error) {
	if len(raw) == 0 || len(raw) > 16384 || !json.Valid(raw) {
		return fmt.Errorf("bounded complete JSON readiness required")
	}
	directory := filepath.Dir(path)
	stage, e := os.CreateTemp(directory, ".arm-ready-*")
	if e != nil {
		return e
	}
	name := stage.Name()
	born, e := stage.Stat()
	if e != nil {
		stage.Close()
		return e
	}
	closed := false
	defer func() {
		if !closed {
			closed = true
			err = errors.Join(err, stage.Close())
		}
		current, e := os.Lstat(name)
		if errors.Is(e, os.ErrNotExist) {
			return
		}
		if e != nil {
			err = errors.Join(err, e)
			return
		}
		if !os.SameFile(born, current) || !current.Mode().IsRegular() {
			err = errors.Join(err, fmt.Errorf("readiness staging identity changed; preserved"))
			return
		}
		err = errors.Join(err, os.Remove(name))
	}()
	if _, e = stage.Write(raw); e != nil {
		return e
	}
	if e = stage.Sync(); e != nil {
		return e
	}
	closed = true
	if e = stage.Close(); e != nil {
		return e
	}
	if beforePublish != nil {
		if e = beforePublish(name); e != nil {
			return e
		}
	}
	current, e := os.Lstat(name)
	if e != nil {
		return e
	}
	if !os.SameFile(born, current) || !current.Mode().IsRegular() || current.Size() != int64(len(raw)) {
		return fmt.Errorf("readiness staging identity changed; preserved")
	}
	// Same-directory hard-link publication is atomic and refuses every existing
	// final entry, including a symlink. Rename would silently replace a sentinel.
	if e = os.Link(name, path); e != nil {
		return e
	}
	dir, e := os.Open(directory)
	if e != nil {
		return e
	}
	syncErr := dir.Sync()
	return errors.Join(syncErr, dir.Close())
}
