package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

var ErrAuthorityBusy = errors.New("live backend owns authority lock")

var databaseNames = []string{"state.db", "state.db-wal", "state.db-shm"}

type Source struct {
	Directory    *Directory
	Files        []File
	lock         *os.File
	DB           *sql.DB
	temp         string
	tempIdentity File
	live         bool
}

func (s *Source) Close() (err error) {
	if s == nil {
		return nil
	}
	if s.DB != nil {
		err = errors.Join(err, s.DB.Close())
		s.DB = nil
	}
	if s.lock != nil {
		err = errors.Join(err, unix.Flock(int(s.lock.Fd()), unix.LOCK_UN), s.lock.Close())
		s.lock = nil
	}
	if s.Directory != nil {
		err = errors.Join(err, s.Directory.Close())
		s.Directory = nil
	}
	if s.temp == "" {
		return err
	}
	temp, e := OpenDirectory(s.temp)
	if e != nil {
		return errors.Join(err, e)
	}
	defer temp.Close()
	if !sameIdentity(s.tempIdentity, temp.identity) {
		return errors.Join(err, fmt.Errorf("private maintenance temporary directory changed; no cleanup attempted: %s", s.temp))
	}
	for _, name := range []string{"state.db", "state.db-wal", "state.db-shm", "state.db-journal"} {
		file, observeErr := temp.Observe(name, false)
		if observeErr != nil {
			return errors.Join(err, observeErr)
		}
		if file.Present {
			if e = temp.Validate(); e != nil {
				return errors.Join(err, e)
			}
			if e = unix.Unlinkat(int(temp.FD.Fd()), name, 0); e != nil {
				return errors.Join(err, e)
			}
		}
	}
	if e = temp.Validate(); e != nil {
		return errors.Join(err, e)
	}
	if e = os.Remove(s.temp); e != nil {
		return errors.Join(err, fmt.Errorf("private maintenance temporary directory retained at %s: %w", s.temp, e))
	}
	s.temp = ""
	return err
}
func sourceInventory(ctx context.Context, d *Directory, hash bool) ([]File, error) {
	var result []File
	var total int64
	for _, name := range databaseNames {
		f, v, err := d.openSource(name)
		if err != nil {
			return nil, err
		}
		if f != nil {
			if hash {
				v.SHA256, err = hashFile(ctx, f)
			}
			f.Close()
			if err != nil {
				return nil, err
			}
		}
		total += v.Size
		if total > MaxDatabaseBytes {
			return nil, fmt.Errorf("source exceeds 8 GiB maintenance bound")
		}
		result = append(result, v)
	}
	if !result[0].Present {
		return nil, fmt.Errorf("existing state.db required; no database was created")
	}
	if journal, err := d.Observe("state.db-journal", false); err != nil || journal.Present {
		return nil, fmt.Errorf("unexpected rollback journal; preserve source and recover through ARM before maintenance")
	}
	return result, nil
}
func (s *Source) Validate(ctx context.Context, exact bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Directory.Validate(); err != nil {
		return err
	}
	now, err := sourceInventory(ctx, s.Directory, exact)
	if err != nil {
		return err
	}
	for i, old := range s.Files {
		if exact {
			if !reflect.DeepEqual(old, now[i]) {
				return fmt.Errorf("database file changed since inspection: %s", old.Name)
			}
		} else if !sameIdentity(old, now[i]) {
			return fmt.Errorf("database file identity changed during maintenance: %s", old.Name)
		}
	}
	return nil
}
func sqliteURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(50)"}).String()
}
func openRO(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Live uses its own read-only connection, never the manager's writer pool.
// The existing authority keeps the WAL index alive; missing companions refuse
// this path instead of asking SQLite to create shared-memory files.
func Live(ctx context.Context, home string) (s *Source, err error) {
	d, err := OpenDirectory(home)
	if err != nil {
		return nil, err
	}
	s = &Source{Directory: d, live: true}
	defer func(opened *Source) {
		if err != nil {
			opened.Close()
		}
	}(s)
	s.Files, err = sourceInventory(ctx, d, false)
	if err != nil {
		return nil, err
	}
	if !s.Files[1].Present || !s.Files[2].Present {
		return nil, fmt.Errorf("live WAL/shared-memory evidence is missing; use stopped maintenance rather than creating source files")
	}
	s.DB, err = openRO(filepath.Join(home, "state.db"))
	if err == nil {
		err = s.DB.PingContext(ctx)
	}
	if err == nil {
		err = s.Validate(ctx, false)
	}
	return s, err
}

// Offline pins the stopped authority and copies only stable main/WAL files.
// SQLite may build its own WAL index in private OS-temp, never in ARM home.
func Offline(ctx context.Context, home string) (*Source, error) {
	return offline(ctx, home, nil)
}

// OfflineHeld reuses the caller's retained daemon.lock; it never unlocks or
// closes that descriptor. The caller must keep the exclusive lock through all
// observations and the following startup/close transaction.
func OfflineHeld(ctx context.Context, home string, held *os.File) (*Source, error) {
	if held == nil {
		return nil, fmt.Errorf("retained daemon lock required")
	}
	return offline(ctx, home, held)
}

func offline(ctx context.Context, home string, retained *os.File) (s *Source, err error) {
	d, err := OpenDirectory(home)
	if err != nil {
		return nil, err
	}
	s = &Source{Directory: d}
	defer func(opened *Source) {
		if err != nil {
			opened.Close()
		}
	}(s)
	authority := retained
	if authority == nil {
		s.lock, _, err = d.Open("daemon.lock", unix.O_RDONLY)
		if err != nil {
			return nil, err
		}
		authority = s.lock
	}
	if authority == nil {
		return nil, fmt.Errorf("existing authority lock is required")
	}
	// Acquiring on the retained open-file description is idempotent. Failure
	// never falls back to a named PID or an independently opened lock.
	if err = unix.Flock(int(authority.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w", ErrAuthorityBusy)
		}
		return nil, err
	}
	named, err := d.Observe("daemon.lock", false)
	if err != nil {
		return nil, err
	}
	held, err := nativeFile(authority, "daemon.lock", false)
	if err != nil || !sameIdentity(named, held) {
		return nil, fmt.Errorf("authority lock identity changed")
	}
	s.Files, err = sourceInventory(ctx, d, true)
	if err != nil {
		return nil, err
	}
	temp, err := os.MkdirTemp("", "arm-database-")
	if err != nil {
		return nil, err
	}
	temp, err = filepath.EvalSymlinks(temp)
	if err != nil {
		return nil, err
	}
	s.temp = temp
	temporary, e := OpenDirectory(temp)
	if e != nil {
		return nil, e
	}
	s.tempIdentity = temporary.identity
	temporary.Close()
	for _, v := range s.Files[:2] {
		if !v.Present {
			continue
		}
		f, _, e := d.openSource(v.Name)
		if e != nil {
			return nil, e
		}
		out, e := os.OpenFile(filepath.Join(temp, v.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			e = copyContext(ctx, out, f, v.Size)
		}
		f.Close()
		if out != nil {
			if closeErr := out.Close(); e == nil {
				e = closeErr
			}
		}
		if e != nil {
			return nil, e
		}
	}
	if err = s.Validate(ctx, true); err != nil {
		return nil, err
	}
	s.DB, err = openRO(filepath.Join(temp, "state.db"))
	if err == nil {
		err = s.DB.PingContext(ctx)
	}
	return s, err
}
func copyContext(ctx context.Context, out io.Writer, in io.Reader, size int64) error {
	buf := make([]byte, 64<<10)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := in.Read(buf)
		if n > 0 {
			copied += int64(n)
			if copied > size || copied > MaxDatabaseBytes {
				return fmt.Errorf("source grew while copying")
			}
			if _, e := out.Write(buf[:n]); e != nil {
				return e
			}
		}
		if err == io.EOF {
			if copied != size {
				return fmt.Errorf("source shrank while copying")
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}
