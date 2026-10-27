package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
)

type Backup struct {
	CustomNames *CustomNamesInspection `json:"custom_names,omitempty"`
	Destination string                 `json:"destination"`
	Host        string                 `json:"host"`
	Size        int64                  `json:"size_bytes"`
	SHA256      string                 `json:"sha256"`
	Started     time.Time              `json:"started_at"`
	Finished    time.Time              `json:"finished_at"`
	Scope       string                 `json:"scope"`
	Detail      string                 `json:"detail"`
}
type backupConnection interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func BackupDatabase(ctx context.Context, source *Source, destination string) (Backup, error) {
	return backupDatabase(ctx, source, destination, nil)
}
func backupDatabase(ctx context.Context, source *Source, destination string, observe func(string)) (result Backup, err error) {
	result = Backup{Destination: destination, Started: SnapshotTime(), Scope: "consistent SQLite ledger only", Detail: "Configuration, mesh keys/trust, managed artifacts, external outputs and pending client intents are not included. Source preserved; no compaction or repair."}
	defer func() { result.Finished = SnapshotTime() }()
	if !filepath.IsAbs(destination) || !safeName(filepath.Base(destination)) {
		return result, fmt.Errorf("backup requires an absolute unused destination file")
	}
	parent, err := OpenDirectory(filepath.Dir(destination))
	if err != nil {
		return result, err
	}
	defer parent.Close()
	name := filepath.Base(destination)
	if fd, v, e := parent.Open(name, unix.O_RDONLY); e != nil {
		return result, e
	} else if fd != nil {
		fd.Close()
		return result, fmt.Errorf("backup destination exists (device%d inode%d); nothing overwritten", v.Device, v.Inode)
	}
	temp := ".arm-database-backup-" + rand.Text()
	f, created, err := parent.Open(temp, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
	if err != nil {
		return result, err
	}
	if f == nil {
		return result, fmt.Errorf("backup staging file not created")
	}
	f.Close()
	published := false
	defer func() {
		if published {
			return
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			companion, e := parent.Observe(temp+suffix, false)
			if e != nil || companion.Present {
				err = errors.Join(err, fmt.Errorf("private staging bundle retained for inspection; SQLite companion was not deleted: %s", filepath.Join(parent.Path, temp)))
				return
			}
		}
		current, e := parent.Observe(temp, false)
		if e == nil && sameIdentity(created, current) {
			if unlinkErr := unix.Unlinkat(int(parent.FD.Fd()), temp, 0); unlinkErr != nil {
				err = errors.Join(err, fmt.Errorf("private backup staging cleanup failed at %s: %w", filepath.Join(parent.Path, temp), unlinkErr))
			}
		} else if e != nil || current.Present {
			err = errors.Join(err, fmt.Errorf("private backup staging retained for inspection: %s", filepath.Join(parent.Path, temp)))
		}
	}()
	conn, err := source.DB.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	var pageSize int64
	if err = conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return result, err
	}
	if pageSize <= 0 {
		return result, fmt.Errorf("invalid SQLite page size")
	}
	if err = conn.QueryRowContext(ctx, "SELECT substr(value,1,33) FROM metadata WHERE key='host'").Scan(&result.Host); err != nil {
		return result, err
	}
	if !validHost(result.Host) {
		return result, fmt.Errorf("invalid source authority; backup not published")
	}
	target := (&url.URL{Scheme: "file", Path: filepath.Join(parent.Path, temp), RawQuery: "mode=rw&_pragma=busy_timeout(50)"}).String()
	err = conn.Raw(func(raw any) (callErr error) {
		connection, ok := raw.(backupConnection)
		if !ok {
			return fmt.Errorf("SQLite online backup API unavailable")
		}
		backup, e := connection.NewBackup(target)
		if e != nil {
			return e
		}
		defer func() { callErr = errors.Join(callErr, backup.Finish()) }()
		for {
			if e := ctx.Err(); e != nil {
				return e
			}
			more, e := backup.Step(32)
			if observe != nil {
				observe("step")
			}
			if e != nil {
				return e
			}
			if int64(backup.PageCount()) > MaxDatabaseBytes/pageSize {
				return fmt.Errorf("backup exceeds 8 GiB maintenance bound")
			}
			if !more {
				return nil
			}
			runtime.Gosched()
		}
	})
	if err != nil {
		return result, err
	}
	if err = source.Validate(ctx, !source.live); err != nil {
		return result, err
	}
	if err = parent.Validate(); err != nil {
		return result, err
	}
	// Make the new output standalone. This changes only the private destination
	// copy, never the source journal policy or its WAL.
	verify, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.Join(parent.Path, temp), RawQuery: "mode=rw"}).String())
	if err != nil {
		return result, err
	}
	verify.SetMaxOpenConns(1)
	var journal, check, host string
	err = verify.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&journal)
	if err == nil {
		err = verify.QueryRowContext(ctx, "PRAGMA quick_check(100)").Scan(&check)
	}
	if err == nil && check != "ok" {
		err = fmt.Errorf("new backup failed SQLite quick check")
	}
	if err == nil {
		err = verify.QueryRowContext(ctx, "SELECT substr(value,1,33) FROM metadata WHERE key='host'").Scan(&host)
	}
	if err == nil && host != result.Host {
		err = fmt.Errorf("backup authority differs from source snapshot")
	}
	if err == nil {
		names, namesErr := InspectCustomNames(ctx, verify, strconv.Itoa(os.Geteuid()), result.Host)
		if namesErr != nil {
			err = namesErr
		} else {
			result.CustomNames = &names
		}
	}
	closeErr := verify.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return result, err
	}
	if host != result.Host {
		return result, fmt.Errorf("backup authority differs from source snapshot")
	}
	staged, version, err := parent.Open(temp, unix.O_RDONLY)
	if err != nil {
		return result, err
	}
	if staged == nil || !sameIdentity(created, version) {
		if staged != nil {
			staged.Close()
		}
		return result, fmt.Errorf("backup staging identity changed")
	}
	result.SHA256, err = hashFile(ctx, staged)
	if err == nil {
		err = staged.Sync()
	}
	staged.Close()
	if err != nil {
		return result, err
	}
	result.Size = version.Size
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if companion, e := parent.Observe(temp+suffix, false); e != nil || companion.Present {
			return result, fmt.Errorf("backup still has a SQLite companion; publication refused")
		}
	}
	if err = parent.Validate(); err != nil {
		return result, err
	}
	if observe != nil {
		observe("publication")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	current, err := parent.Observe(temp, false)
	if err != nil || !sameIdentity(version, current) || version.Size != current.Size || version.Modified != current.Modified || version.Changed != current.Changed {
		return result, fmt.Errorf("backup staging changed before publication; nothing published")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if companion, e := parent.Observe(temp+suffix, false); e != nil || companion.Present {
			return result, fmt.Errorf("backup staging companion appeared before publication; bundle retained")
		}
	}
	if err = renameExclusive(int(parent.FD.Fd()), temp, name); err != nil {
		return result, err
	}
	published = true
	if err = parent.Sync(); err != nil {
		return result, fmt.Errorf("backup published but parent directory sync failed; retain %s and inspect: %w", destination, err)
	}
	return result, nil
}
