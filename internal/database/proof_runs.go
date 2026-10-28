package database

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

var proxyRunResetSchema = map[string]string{
	"proxy_runs":       `CREATE TABLE proxy_runs (id TEXT PRIMARY KEY, request_key TEXT UNIQUE NOT NULL, request TEXT NOT NULL, data TEXT NOT NULL)`,
	"proxy_run_chunks": `CREATE TABLE proxy_run_chunks (run TEXT NOT NULL, offset INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(run,offset))`,
}

// These are observations of CLI-owned commands, never resource grants or
// authority to signal/clean external processes. Keep a separate reviewed mirror.
type proofProxyRun struct {
	model.Run
	LastOffset int64  `json:"last_offset"`
	LastSize   int    `json:"last_size"`
	LastSHA256 string `json:"last_sha256"`
}

func (p *proofReader) readProxyRuns() error {
	runs := map[string]proofProxyRun{}
	if err := p.walk("proxy_runs", func(row proofRow) error {
		if len(runs) >= 4096 {
			return fmt.Errorf("proxy run count exceeds bound")
		}
		rec, err := rowJSON[proofProxyRun](row, "data", "id", "name", "command", "cwd", "reason", "host", "state", "created_at", "last_reported_at", "log_bytes", "accepted_bytes", "truncated", "last_offset", "last_size", "last_sha256")
		if err != nil {
			return err
		}
		q, err := rowJSON[model.RunCreateRequest](row, "request", "key", "command", "cwd", "reason")
		if err != nil {
			return err
		}
		id, err := row.text("id")
		if err != nil {
			return err
		}
		key, err := row.text("request_key")
		if err != nil {
			return err
		}
		if !sshProofHex(id, 16) || rec.ID != id || rec.Name != readablename.FromID("run", id) || rec.LeaseID != "" || rec.Host != p.result.Host || q.Key != key || key == "" || len(key) > 128 || !reflect.DeepEqual(q.Command, rec.Command) || q.CWD != rec.CWD || q.Agent != rec.Agent || q.Reason != rec.Reason || len(rec.Command) == 0 || len(rec.Command) > 256 || rec.Command[0] == "" || !filepath.IsAbs(rec.CWD) || len(rec.CWD) > 4096 || len(rec.Agent) > 256 || strings.TrimSpace(rec.Reason) == "" || len(rec.Reason) > 4096 || rec.Created.IsZero() || rec.Started == nil || !rec.Started.Equal(rec.Created) || rec.LastReported.Before(rec.Created) || rec.LogBytes < 0 || rec.LogBytes > 64<<20 || rec.AcceptedBytes < rec.LogBytes || rec.LastOffset < -1 || rec.LastSize < 0 || rec.LastSize > 64<<10 {
			return fmt.Errorf("invalid proxy run identity or bounds")
		}
		if !rec.Truncated && rec.LogBytes != rec.AcceptedBytes {
			return fmt.Errorf("unreported log truncation")
		}
		if rec.AcceptedBytes == 0 {
			if rec.LastOffset != -1 || rec.LastSize != 0 || rec.LastSHA256 != "" {
				return fmt.Errorf("invalid initial append receipt")
			}
		} else if rec.LastOffset < 0 || rec.LastOffset+int64(rec.LastSize) != rec.AcceptedBytes || rec.LastSize == 0 || !sshProofHex(rec.LastSHA256, 32) {
			return fmt.Errorf("invalid append receipt")
		}
		switch rec.State {
		case "active", "interrupted":
			if rec.Ended != nil || rec.ExitCode != nil {
				return fmt.Errorf("lost reporter asserts an exit")
			}
			if rec.State == "interrupted" && rec.Outcome != "reporter_lost" || rec.State == "active" && rec.Outcome != "" {
				return fmt.Errorf("invalid reporter outcome")
			}
			if err = p.block("proxy_run", id, "CLI-owned execution has no acknowledged final report; reporter loss never proves process exit"); err != nil {
				return err
			}
		case "terminal":
			if rec.Ended == nil || rec.Ended.Before(rec.Created) || rec.ExitCode == nil || *rec.ExitCode < 0 || *rec.ExitCode > 255 || !oneOf(rec.Outcome, "completed", "failed", "cancelled", "launch_failed", "log_failed") {
				return fmt.Errorf("invalid reported run result")
			}
		default:
			return fmt.Errorf("unsupported proxy run state")
		}
		runs[id] = rec
		return nil
	}); err != nil {
		return err
	}
	offsets := map[string]int64{}
	counts := map[string]int{}
	total := int64(0)
	chunks := 0
	if err := p.walk("proxy_run_chunks", func(row proofRow) error {
		id, err := row.text("run")
		if err != nil {
			return err
		}
		if _, ok := runs[id]; !ok {
			return fmt.Errorf("orphan proxy log chunk")
		}
		offset, err := row.integer("offset")
		if err != nil {
			return err
		}
		raw, err := row.text("data")
		if err != nil {
			return err
		}
		if len(raw) > 87384 {
			return fmt.Errorf("proxy chunk exceeds bound")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(raw)
		if err != nil || len(data) == 0 || len(data) > 64<<10 || offset != offsets[id] {
			return fmt.Errorf("invalid or noncontiguous proxy log chunk")
		}
		offsets[id] += int64(len(data))
		counts[id]++
		chunks++
		total += int64(len(data))
		if counts[id] > 4096 || chunks > 16384 || total > 256<<20 {
			return fmt.Errorf("proxy log storage exceeds bound")
		}
		return nil
	}); err != nil {
		return err
	}
	for id, rec := range runs {
		if offsets[id] != rec.LogBytes {
			return fmt.Errorf("proxy log does not match acknowledged retained bytes")
		}
	}
	return nil
}
