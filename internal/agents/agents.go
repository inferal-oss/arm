package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/model"
)

type Contact struct {
	ID           string `json:"id"`
	Adapter      string `json:"adapter"`
	Session      string `json:"session"`
	CWD          string `json:"cwd"`
	ConfigDir    string `json:"config_dir,omitempty"`
	Remote       string `json:"remote,omitempty"`
	Idle         bool   `json:"idle,omitempty"`
	Delivery     string `json:"delivery,omitempty"`
	Registration string `json:"registration,omitempty"`
}
type Capability struct {
	Adapter  string `json:"adapter"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Mode     string `json:"mode"`
	Error    string `json:"error,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}
type Delivery struct {
	State        string   `json:"state"`
	Error        string   `json:"error,omitempty"`
	Reply        string   `json:"reply,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Registration string   `json:"registration,omitempty"`
	Receipt      *Receipt `json:"receipt,omitempty"`
}

type Receipt struct {
	Received     *time.Time `json:"received_at,omitempty"`
	Acknowledged *time.Time `json:"acknowledged_at,omitempty"`
	Text         string     `json:"text,omitempty"`
	ReplyID      string     `json:"reply_id,omitempty"`
}

func Binary(name string) (string, error) {
	if name != "codex" && name != "claude" {
		return "", model.Err("unsupported_adapter", "expected codex or claude")
	}
	if p, e := exec.LookPath(name); e == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "bin", name)
	if fi, e := os.Stat(p); e == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
		return p, nil
	}
	return "", model.Err("cli_missing", name+" is not installed or executable")
}
func Probe(ctx context.Context, name string) Capability {
	c := Capability{Adapter: name, Fallback: "polling-inbox"}
	if name == "generic" {
		c.Mode = "polling-inbox"
		return c
	}
	p, e := Binary(name)
	if e != nil {
		c.Error = e.Error()
		return c
	}
	c.Path = p
	b, e := exec.CommandContext(ctx, p, "--version").Output()
	if e != nil {
		c.Error = e.Error()
		return c
	}
	c.Version = strings.TrimSpace(string(b))
	args := []string{"--help"}
	if name == "codex" {
		args = []string{"queue", "--help"}
	}
	b, e = exec.CommandContext(ctx, p, args...).Output()
	if e != nil {
		c.Error = e.Error()
		return c
	}
	if name == "codex" && strings.Contains(string(b), "--thread") && strings.Contains(string(b), "--message") {
		c.Mode = "queue-existing-session"
	} else if name == "claude" && strings.Contains(string(b), "--resume") {
		c.Mode = "resume-explicitly-idle-session"
	} else {
		c.Error = "installed CLI does not expose the required messaging interface"
	}
	return c
}

func (c Contact) Validate() error {
	if c.ID == "" || c.ID == "operator" || c.ID == "broadcast" || len(c.ID) > 128 {
		return model.Err("invalid_contact", "a short contact ID is required")
	}
	if _, e := uuid.Parse(c.Session); e != nil {
		return model.Err("invalid_contact", "an exact session UUID is required")
	}
	if !filepath.IsAbs(c.CWD) {
		return model.Err("invalid_contact", "cwd must be absolute")
	}
	if fi, e := os.Stat(c.CWD); e != nil || !fi.IsDir() {
		return model.Err("invalid_contact", "cwd is not a directory")
	}
	if c.ConfigDir != "" && !filepath.IsAbs(c.ConfigDir) {
		return model.Err("invalid_contact", "config-dir must be absolute")
	}
	if c.Delivery != "" && c.Delivery != "native" && c.Delivery != "poll" {
		return model.Err("unsupported_delivery", "delivery must be native or poll")
	}
	if c.Adapter == "claude" && !c.Idle && c.Delivery != "poll" {
		return model.Err("unsupported_capability", "Claude adapter currently requires an explicitly idle session; live injection is not implemented")
	}
	if c.Adapter != "claude" && c.Adapter != "codex" && !(c.Adapter == "generic" && c.Delivery == "poll") {
		return model.Err("unsupported_adapter", "expected codex, claude, or generic with poll delivery")
	}
	if c.Remote != "" && !strings.HasPrefix(c.Remote, "unix://") {
		return model.Err("invalid_contact", "only local unix Codex endpoints are supported")
	}
	if c.Remote != "" && c.Adapter != "codex" {
		return model.Err("invalid_contact", "remote endpoints apply only to Codex")
	}
	return nil
}

func (c Contact) env() []string {
	env := os.Environ()
	key := "CODEX_HOME"
	if c.Adapter == "claude" {
		key = "CLAUDE_CONFIG_DIR"
	}
	if c.ConfigDir != "" {
		filtered := env[:0]
		for _, v := range env {
			if !strings.HasPrefix(v, key+"=") {
				filtered = append(filtered, v)
			}
		}
		env = append(filtered, key+"="+c.ConfigDir)
	}
	return env
}

func Deliver(ctx context.Context, c Contact, body string) Delivery {
	if e := c.Validate(); e != nil {
		return Delivery{State: "failed", Error: e.Error()}
	}
	if c.Delivery == "poll" {
		return Delivery{State: "available", Mode: "poll", Registration: c.Registration}
	}
	p, e := Binary(c.Adapter)
	if e != nil {
		return Delivery{State: "failed", Error: e.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if c.Adapter == "codex" {
		args := []string{"queue", "--thread", c.Session, "--message", body}
		if c.Remote != "" {
			args = append(args, "--remote", c.Remote)
		}
		cmd = exec.CommandContext(ctx, p, args...)
	} else {
		// Never silently create a new Claude session when an ID is mistyped.
		root := c.ConfigDir
		if root == "" {
			root = os.Getenv("CLAUDE_CONFIG_DIR")
		}
		if root == "" {
			h, _ := os.UserHomeDir()
			root = filepath.Join(h, ".claude")
		}
		found := false
		_ = filepath.WalkDir(filepath.Join(root, "projects"), func(path string, d os.DirEntry, e error) error {
			if e == nil && !d.IsDir() && d.Name() == c.Session+".jsonl" {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if !found {
			return Delivery{State: "failed", Error: "existing Claude session transcript was not found; refusing to create another session"}
		}
		cmd = exec.CommandContext(ctx, p, "-p", "--resume", c.Session, "--output-format", "json", "--tools", "", "--strict-mcp-config", "--setting-sources", "")
		cmd.Stdin = strings.NewReader(body)
	}
	cmd.Dir = c.CWD
	cmd.Env = c.env()
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	e = cmd.Run()
	if e != nil {
		return Delivery{State: "failed", Error: fmt.Sprintf("%s: %s", e, strings.TrimSpace(stderr.String()))}
	}
	if c.Adapter == "codex" {
		return Delivery{State: "queued"}
	}
	var result struct {
		Session string `json:"session_id"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if e = json.Unmarshal(stdout.Bytes(), &result); e != nil || result.IsError || result.Session != c.Session {
		return Delivery{State: "failed", Error: "Claude did not confirm a successful result in the registered session"}
	}
	return Delivery{State: "responded", Reply: result.Result}
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 65536 {
		remaining := 65536 - b.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
