package agentsessions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Codex connects only to an already-running native control socket via the
// installed proxy command. It never starts/resumes threads or an app server.
type Codex struct {
	Home, Binary string
	Roots        []string
}

func (Codex) Name() string { return "codex" }
func (c Codex) Discover(ctx context.Context) Result {
	return c.discover(ctx, nil)
}

// DiscoverTitles reuses the bounded metadata-only reader without exposing titles
// through session discovery or changing its identity contract.
func (c Codex) DiscoverTitles(ctx context.Context, roots []string) TitleResult {
	c.Roots = roots
	names := map[string]string{}
	result := c.discover(ctx, names)
	out := TitleResult{Warnings: result.Warnings}
	for _, session := range result.Sessions {
		if name := names[session.ID]; name != "" {
			out.Titles = append(out.Titles, Title{Directory: session.CWD, Name: name, Updated: session.Updated})
		}
	}
	return out
}

func (c Codex) discover(ctx context.Context, names map[string]string) Result {
	out := Result{Adapter: "codex", Sessions: []Session{}, Warnings: []string{}}
	fail := func() Result {
		out.Sessions = []Session{}
		out.Warnings = append(out.Warnings, "Codex native metadata is unavailable or unsupported; an existing app-server control socket and proxy interface are required.")
		return out
	}
	if fi, e := os.Stat(c.Home); e != nil || !fi.IsDir() || !filepath.IsAbs(c.Home) || !filepath.IsAbs(c.Binary) {
		return fail()
	}
	if len(c.Roots) == 0 || len(c.Roots) > 16 {
		return fail()
	}
	for _, root := range c.Roots {
		if !filepath.IsAbs(root) {
			return fail()
		}
	}
	canonicalHome, err := filepath.EvalSymlinks(c.Home)
	if err != nil {
		return fail()
	}
	c.Home = canonicalHome
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Binary, "app-server", "proxy")
	cmd.Env = Environment("CODEX_HOME", c.Home)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		return fail()
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		stdin.Close()
		return fail()
	}
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		stdin.Close()
		return fail()
	}
	defer func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(io.LimitReader(stdout, 8<<20))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	encoder := json.NewEncoder(stdin)
	seq := 0
	messages := 0
	call := func(method string, params any, result any) error {
		seq++
		if e := encoder.Encode(map[string]any{"id": seq, "method": method, "params": params}); e != nil {
			return e
		}
		for scanner.Scan() {
			messages++
			if messages > 128 {
				return fmt.Errorf("native response budget exceeded")
			}
			var response struct {
				ID     int             `json:"id"`
				Error  json.RawMessage `json:"error"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(scanner.Bytes(), &response) != nil {
				return fmt.Errorf("unsupported native metadata")
			}
			if response.ID != seq {
				continue
			}
			if len(response.Error) > 0 && string(response.Error) != "null" {
				return fmt.Errorf("native metadata request refused")
			}
			return json.Unmarshal(response.Result, result)
		}
		return fmt.Errorf("native metadata stream ended")
	}
	var initialized json.RawMessage
	if call("initialize", map[string]any{"clientInfo": map[string]string{"name": "arm_rollout", "version": "1"}}, &initialized) != nil {
		return fail()
	}
	if encoder.Encode(map[string]any{"method": "initialized"}) != nil {
		return fail()
	}
	cursor := ""
	seen := map[string]string{}
	for page := 0; page < 6; page++ {
		var response struct {
			Data []struct {
				ID      string          `json:"id"`
				Name    json.RawMessage `json:"name"`
				CWD     string          `json:"cwd"`
				Created json.RawMessage `json:"createdAt"`
				Updated json.RawMessage `json:"updatedAt"`
				Status  struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		params := map[string]any{"limit": 100, "archived": false, "useStateDbOnly": true, "sortKey": "updated_at", "sortDirection": "desc", "cwd": c.Roots}
		if names != nil {
			// Desktop tasks can use appServer rather than the native default
			// cli/vscode source kinds. This affects display metadata only.
			params["sourceKinds"] = []string{"cli", "vscode", "appServer"}
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if call("thread/list", params, &response) != nil {
			return fail()
		}
		for _, thread := range response.Data {
			if len(out.Sessions) >= Limit {
				out.Warnings = append(out.Warnings, "Codex session discovery is truncated.")
				return out
			}
			s, e := Normalize(Session{Adapter: "codex", ID: thread.ID, CWD: thread.CWD, Home: c.Home, Started: timestamp(thread.Created), Updated: timestamp(thread.Updated), Activity: thread.Status.Type, Evidence: "native thread metadata; status is an observation, not liveness proof"})
			if e == nil && seen[s.ID] != "" && seen[s.ID] != s.Generation {
				return fail()
			}
			if e == nil && seen[s.ID] == "" {
				out.Sessions = append(out.Sessions, s)
				seen[s.ID] = s.Generation
				if names != nil {
					var name string
					if json.Unmarshal(thread.Name, &name) == nil {
						names[s.ID] = name
					}
				}
			}
		}
		if response.Next == "" {
			return out
		}
		if response.Next == cursor {
			return fail()
		}
		cursor = response.Next
	}
	out.Warnings = append(out.Warnings, "Codex session discovery is truncated.")
	return out
}

func Environment(key, value string) []string {
	env := []string{}
	for _, s := range os.Environ() {
		if len(s) > len(key) && s[:len(key)+1] == key+"=" {
			continue
		}
		env = append(env, s)
	}
	return append(env, key+"="+value)
}
