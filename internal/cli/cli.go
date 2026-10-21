package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/install"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/server"
)

const Version = "0.1.0-dev"

type app struct {
	home     string
	in       io.Reader
	out, err io.Writer
	ctx      context.Context
}

func Main(args []string, in io.Reader, out, errout io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := app{home: local.Home(), in: in, out: out, err: errout, ctx: ctx}
	f := a.flags("arm")
	f.StringVar(&a.home, "home", a.home, "private ARM state directory (or ARM_HOME)")
	if e := f.Parse(args); e != nil {
		return 2
	}
	abs, e := filepath.Abs(a.home)
	if e != nil {
		fmt.Fprintln(errout, e)
		return 1
	}
	a.home = abs
	args = f.Args()
	if len(args) == 0 {
		a.help()
		return 0
	}
	code, e := a.command(args)
	if a.ctx.Err() != nil {
		return 130
	}
	if errors.Is(e, flag.ErrHelp) {
		return 0
	}
	if e != nil {
		var api *model.Error
		if errors.As(e, &api) {
			_ = json.NewEncoder(errout).Encode(map[string]any{"error": api})
		} else {
			fmt.Fprintln(errout, "arm:", e)
		}
		if code == 0 {
			code = 1
		}
	}
	return code
}
func (a *app) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.err)
	return f
}
func (a *app) print(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func (a *app) call(op string, p, result any) error {
	ctx, cancel := context.WithTimeout(a.ctx, 75*time.Second)
	defer cancel()
	return client.Call(ctx, a.home, op, p, result)
}
func (a *app) help() {
	fmt.Fprintln(a.out, `Agent Resource Manager
Usage: arm [--home DIR] COMMAND [OPTIONS]

Commands:
  daemon start|stop|status    Manage the local backend
  serve                     Run the backend in the foreground
  status                    Show host budgets and leases
  resources                 Show budgets, availability, owners, and reasons
  storage create|list|inspect|cleanup|handoff  Manage storage lifetimes
  acquire                   Request a resource lease
  run                       Execute a command under a supervised lease
  inspect|release|cancel ID  Inspect or end a lease
  reconcile ID              Verify a recovered process group has stopped
  renew ID --ttl DURATION    Extend a lease when the queue permits
  events [--follow]          Read or stream durable events
  agent probe|register|list  Discover CLIs and register exact sessions
  message operator|send|broadcast  Send a durable message
  messages                  Read the operator inbox and delivery status
  install|uninstall         Manage reversible agent instruction blocks
  version                   Print build and Go versions

Use COMMAND --help for options. Status operations return JSON.
This initial backend uses cooperative CPU/memory/disk budgets, not OS quotas.`)
}

func (a *app) command(args []string) (int, error) {
	cmd, args := args[0], args[1:]
	switch cmd {
	case "help":
		a.help()
		return 0, nil
	case "version":
		return 0, a.print(map[string]string{"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH})
	case "serve", "daemon":
		return a.daemon(cmd, args)
	case "acquire", "run":
		return a.acquire(cmd, args)
	case "install", "uninstall":
		return 0, a.integration(cmd, args)
	case "agent":
		return 0, a.agent(args)
	case "message":
		return 0, a.message(args)
	case "storage":
		return a.storage(args)
	case "events":
		f := a.flags(cmd)
		follow := f.Bool("follow", false, "stream live events")
		after := f.Int64("after", 0, "resume after this event sequence")
		f.Bool("json", true, "JSON output")
		if e := f.Parse(args); e != nil {
			return 2, e
		}
		if *follow {
			return 0, client.Follow(a.ctx, a.home, *after, func(ev model.Event) error { return json.NewEncoder(a.out).Encode(ev) })
		}
		var result any
		if e := a.call("events", map[string]any{"after": *after}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "status", "resources", "messages":
		f := a.flags(cmd)
		f.Bool("json", true, "JSON output")
		host := f.String("host", "localhost", "host ID or localhost")
		if e := f.Parse(args); e != nil {
			return 2, e
		}
		if *host != "localhost" {
			var status model.Status
			if e := a.call("status", nil, &status); e != nil {
				return 1, e
			}
			if status.Host != *host {
				return 1, model.Err("host_unavailable", "only the local host is registered")
			}
		}
		var result any
		if e := a.call(cmd, nil, &result); e != nil {
			return 1, e
		}
		if *host != "localhost" && cmd == "status" {
			s := result.(map[string]any)
			if s["host"] != *host {
				return 1, model.Err("host_unavailable", "only localhost is available")
			}
		}
		return 0, a.print(result)
	case "inspect", "release", "cancel", "reconcile", "renew":
		if len(args) == 0 {
			return 2, fmt.Errorf("%s requires a lease ID", cmd)
		}
		id := args[0]
		f := a.flags(cmd)
		ttl := f.Duration("ttl", 0, "renewal duration from now")
		f.Bool("json", true, "JSON output")
		if e := f.Parse(args[1:]); e != nil {
			return 2, e
		}
		var result model.Lease
		if e := a.call(cmd, map[string]any{"id": id, "ttl_ns": *ttl}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	}
	return 2, fmt.Errorf("unknown command %q", cmd)
}

func bytesValue(s string) (int64, error) {
	s = strings.TrimSpace(s)
	units := []struct {
		name string
		n    float64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1}}
	mult := float64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.name) {
			s = strings.TrimSuffix(s, u.name)
			mult = u.n
			break
		}
	}
	n, e := strconv.ParseFloat(s, 64)
	if e != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) || n*mult > float64(1<<60) {
		return 0, fmt.Errorf("invalid byte quantity %q", s)
	}
	return int64(n * mult), nil
}

func (a *app) acquire(command string, args []string) (int, error) {
	f := a.flags(command)
	defaultCPU := float64(1)
	if command == "storage" {
		defaultCPU = 0
	}
	cpu := f.Float64("cpu", defaultCPU, "logical-core budget (cooperative)")
	memory := f.String("memory", "0", "memory reservation, e.g. 2GiB")
	disk := f.String("disk", "0", "disk reservation, e.g. 10GiB")
	reason := f.String("reason", "", "required purpose")
	ttl := f.Duration("ttl", 10*time.Minute, "lease duration")
	wait := f.Bool("wait", false, "queue and wait for capacity")
	key := f.String("key", "", "idempotency key")
	host := f.String("host", "localhost", "target host")
	profile := f.String("profile", "", "profile (rust-build)")
	project := f.String("project", "", "project attribution")
	agent := f.String("agent", "", "registered agent contact for attribution")
	storageLease := f.String("storage", "", "attach a managed storage lease (supervised run only)")
	cwd := f.String("cwd", "", "command working directory")
	asJSON := f.Bool("json", false, "keep command output on stderr and emit final JSON")
	if e := f.Parse(args); e != nil {
		return 2, e
	}
	mem, e := bytesValue(*memory)
	if e != nil {
		return 2, e
	}
	d, e := bytesValue(*disk)
	if e != nil {
		return 2, e
	}
	if *key == "" {
		*key = local.ID()
	}
	if math.IsNaN(*cpu) || math.IsInf(*cpu, 0) {
		return 2, fmt.Errorf("invalid CPU quantity")
	}
	r := model.Request{Host: *host, Reason: *reason, Project: *project, Agent: *agent, Resources: model.Resources{CPU: *cpu, Memory: mem, Disk: d}, TTL: *ttl, Key: *key, Queue: *wait, Profile: *profile}
	r.StorageLease = *storageLease
	if command == "storage" {
		r.Kind = "storage"
	}
	defer func() {
		if a.ctx.Err() != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = client.Call(ctx, a.home, "cancel_request", map[string]string{"key": r.Key}, nil)
		}
	}()
	var l model.Lease
	if command == "run" {
		argv := f.Args()
		if len(argv) == 0 {
			return 2, fmt.Errorf("run requires -- COMMAND [ARG...]")
		}
		if *cwd == "" {
			*cwd, e = os.Getwd()
			if e != nil {
				return 1, e
			}
		}
		*cwd, e = filepath.Abs(*cwd)
		if e != nil {
			return 2, e
		}
		name := argv[0]
		if strings.ContainsRune(name, os.PathSeparator) && !filepath.IsAbs(name) {
			name = filepath.Join(*cwd, name)
		}
		exe, e := exec.LookPath(name)
		if e != nil {
			return 2, e
		}
		exe, e = filepath.Abs(exe)
		if e != nil {
			return 2, e
		}
		argv[0] = exe
		e = a.call("run", manager.RunRequest{Request: r, Command: argv, CWD: *cwd}, &l)
		if e != nil {
			return 1, e
		}
	} else {
		if e = a.call("acquire", r, &l); e != nil {
			return 1, e
		}
	}
	if command != "run" && (!*wait || l.State != "queued") {
		return a.acquired(l)
	}
	var offset int64
	for {
		if a.ctx.Err() != nil {
			return 130, a.ctx.Err()
		}
		if command == "run" {
			var b []byte
			if e = a.call("output", map[string]any{"id": l.ID, "offset": offset}, &b); e != nil {
				return 1, e
			}
			dest := a.out
			if *asJSON {
				dest = a.err
			}
			if len(b) > 0 {
				if _, e = dest.Write(b); e != nil {
					return 1, e
				}
				offset += int64(len(b))
				continue
			}
		}
		if e = a.call("inspect", map[string]string{"id": l.ID}, &l); e != nil {
			return 1, e
		}
		if command != "run" && l.State != "queued" {
			return a.acquired(l)
		}
		if l.State == "terminal" {
			if command == "run" {
				var b []byte
				if e = a.call("output", map[string]any{"id": l.ID, "offset": offset}, &b); e != nil {
					return 1, e
				}
				if len(b) > 0 {
					dest := a.out
					if *asJSON {
						dest = a.err
					}
					dest.Write(b)
					offset += int64(len(b))
					continue
				}
			}
			if *asJSON {
				if e = a.print(l); e != nil {
					return 1, e
				}
			}
			if l.Outcome == "expired" {
				return 124, nil
			}
			if l.Outcome == "cancelled" || l.Outcome == "revoked" {
				return 125, nil
			}
			if l.ExitCode != nil {
				code := *l.ExitCode
				if code < 0 {
					code = 1
				}
				return code, nil
			}
			if l.Outcome == "launch_failed" {
				return 1, fmt.Errorf("command could not be started; inspect lease %s", l.ID)
			}
			return 0, nil
		}
		if l.State == "needs_attention" {
			return 1, model.Err("needs_attention", "workload supervision requires operator reconciliation")
		}
		select {
		case <-a.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a *app) acquired(l model.Lease) (int, error) {
	if err := a.print(l); err != nil {
		return 1, err
	}
	if l.State == "needs_attention" {
		return 1, model.Err("needs_attention", "allocation remains charged; inspect its failure and ownership details")
	}
	return 0, nil
}

func (a *app) daemon(command string, args []string) (int, error) {
	action := "serve"
	if command == "daemon" {
		if len(args) == 0 {
			return 2, fmt.Errorf("daemon requires start, stop, or status")
		}
		action, args = args[0], args[1:]
	}
	f := a.flags("daemon " + action)
	cpus := f.Float64("cpu", float64(max(1, runtime.NumCPU()-1)), "host CPU reservation budget")
	mem := f.String("memory", "8GiB", "host memory reservation budget")
	disk := f.String("disk", "20GiB", "host disk reservation budget")
	listen := f.String("listen", "127.0.0.1:0", "loopback backend address")
	f.Bool("json", true, "JSON output")
	if e := f.Parse(args); e != nil {
		return 2, e
	}
	if action == "status" {
		var s model.Status
		if e := a.call("status", nil, &s); e != nil {
			return 1, e
		}
		return 0, a.print(s)
	}
	if action == "stop" {
		if e := a.call("shutdown", nil, nil); e != nil {
			return 1, e
		}
		for i := 0; i < 50; i++ {
			if _, e := local.ReadEndpoint(a.home); os.IsNotExist(e) {
				lock, e := os.OpenFile(filepath.Join(a.home, "daemon.lock"), os.O_RDWR, 0600)
				if e != nil {
					return 1, e
				}
				err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
				if err == nil {
					_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
					lock.Close()
					return 0, a.print(map[string]bool{"stopped": true})
				}
				lock.Close()
			}
			time.Sleep(100 * time.Millisecond)
		}
		return 1, fmt.Errorf("backend has not finished shutdown")
	}
	memory, e := bytesValue(*mem)
	if e != nil {
		return 2, e
	}
	d, e := bytesValue(*disk)
	if e != nil {
		return 2, e
	}
	if *cpus <= 0 || math.IsNaN(*cpus) || math.IsInf(*cpus, 0) || memory <= 0 || d <= 0 {
		return 2, fmt.Errorf("host budgets must be positive and finite")
	}
	capacity := model.Resources{CPU: *cpus, Memory: memory, Disk: d}
	if action == "serve" {
		return 0, server.Serve(a.ctx, a.home, *listen, capacity)
	}
	if action != "start" {
		return 2, fmt.Errorf("unknown daemon action")
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Second)
	var existing model.Status
	e = client.Call(ctx, a.home, "status", nil, &existing)
	cancel()
	if e == nil {
		return 0, a.print(map[string]any{"running": true, "host": existing.Host})
	}
	if e = local.Prepare(a.home); e != nil {
		return 1, e
	}
	exe, e := os.Executable()
	if e != nil {
		return 1, e
	}
	log, e := os.OpenFile(filepath.Join(a.home, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return 1, e
	}
	defer log.Close()
	cmd := exec.Command(exe, "--home", a.home, "serve", "--cpu", strconv.FormatFloat(*cpus, 'f', -1, 64), "--memory", strconv.FormatInt(memory, 10), "--disk", strconv.FormatInt(d, 10), "--listen", *listen)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return 1, e
	}
	_ = cmd.Process.Release()
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(a.ctx, 200*time.Millisecond)
		e = client.Call(ctx, a.home, "status", nil, &existing)
		cancel()
		if e == nil {
			return 0, a.print(map[string]any{"running": true, "host": existing.Host, "home": a.home})
		}
		if a.ctx.Err() != nil {
			return 1, a.ctx.Err()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 1, fmt.Errorf("backend did not become ready; inspect %s", filepath.Join(a.home, "daemon.log"))
}

func (a *app) agent(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent requires probe, register, or list")
	}
	action, args := args[0], args[1:]
	f := a.flags("agent " + action)
	var c agents.Contact
	f.StringVar(&c.ID, "id", "", "stable contact name")
	f.StringVar(&c.Adapter, "adapter", "", "codex or claude")
	f.StringVar(&c.Session, "session", "", "exact existing session UUID")
	f.StringVar(&c.CWD, "cwd", "", "session working directory")
	f.StringVar(&c.ConfigDir, "config-dir", "", "agent configuration root")
	f.StringVar(&c.Remote, "remote", "", "Codex unix endpoint for an isolated server")
	f.BoolVar(&c.Idle, "idle", false, "caller declares Claude session idle")
	f.Bool("json", true, "JSON output")
	if e := f.Parse(args); e != nil {
		return e
	}
	if action == "probe" {
		names := []string{"codex", "claude"}
		if c.Adapter != "" {
			names = []string{c.Adapter}
		}
		out := []agents.Capability{}
		for _, name := range names {
			ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
			out = append(out, agents.Probe(ctx, name))
			cancel()
		}
		return a.print(out)
	}
	if action == "list" {
		var out any
		if e := a.call("agents", nil, &out); e != nil {
			return e
		}
		return a.print(out)
	}
	if action != "register" {
		return fmt.Errorf("unknown agent action")
	}
	if c.CWD == "" {
		var e error
		c.CWD, e = os.Getwd()
		if e != nil {
			return e
		}
	}
	var out any
	if e := a.call("register", c, &out); e != nil {
		return e
	}
	return a.print(out)
}

func (a *app) message(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("message requires operator, send, or broadcast")
	}
	action, args := args[0], args[1:]
	f := a.flags("message " + action)
	to := f.String("to", "", "registered contact ID")
	body := f.String("text", "", "message body (or - for stdin)")
	lease := f.String("lease", "", "related lease ID")
	f.Bool("json", true, "JSON output")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *body == "-" {
		b, e := io.ReadAll(io.LimitReader(a.in, 16385))
		if e != nil {
			return e
		}
		*body = string(b)
	}
	audience := action
	if action == "send" {
		audience = *to
		if audience == "" || audience == "operator" || audience == "broadcast" {
			return fmt.Errorf("send requires a registered contact ID")
		}
	} else if action != "operator" && action != "broadcast" {
		return fmt.Errorf("unknown message action")
	}
	var out any
	if e := a.call("message", map[string]string{"audience": audience, "body": *body, "lease": *lease}, &out); e != nil {
		return e
	}
	if e := a.print(out); e != nil {
		return e
	}
	if result, ok := out.(map[string]any); ok {
		if deliveries, ok := result["deliveries"].(map[string]any); ok {
			for _, raw := range deliveries {
				if d, ok := raw.(map[string]any); ok && d["state"] == "failed" {
					return model.Err("delivery_failed", "one or more recipients failed; see the persisted delivery report")
				}
			}
		}
	}
	return nil
}

func (a *app) integration(action string, args []string) error {
	f := a.flags(action)
	dry := f.Bool("dry-run", false, "preview without changes")
	codexRoot := f.String("codex-home", "", "explicit Codex integration root")
	claudeRoot := f.String("claude-home", "", "explicit Claude integration root")
	f.Bool("json", true, "JSON output")
	if e := f.Parse(args); e != nil {
		return e
	}
	if action == "install" {
		userHome, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		targets := []install.Target{}
		for _, name := range []string{"codex", "claude"} {
			root := *codexRoot
			filename := "AGENTS.md"
			if name == "claude" {
				root = *claudeRoot
				filename = "CLAUDE.md"
			}
			if root == "" {
				if _, e := agents.Binary(name); e != nil {
					continue
				}
				root = os.Getenv("CODEX_HOME")
				if name == "claude" {
					root = os.Getenv("CLAUDE_CONFIG_DIR")
				}
				if root == "" {
					root = filepath.Join(userHome, "."+name)
				}
			}
			root, e = filepath.Abs(root)
			if e != nil {
				return e
			}
			targets = append(targets, install.Target{Adapter: name, Root: root, Name: filename})
		}
		if len(targets) == 0 {
			return model.Err("cli_missing", "no supported agent CLIs were found")
		}
		report, e := install.Install(a.home, targets, *dry)
		if e != nil {
			return e
		}
		return a.print(report)
	}
	if e := a.uninstallGuard(*dry); e != nil {
		return e
	}
	if !*dry {
		if e := local.Prepare(a.home); e != nil {
			return e
		}
		lock, e := os.OpenFile(filepath.Join(a.home, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		defer lock.Close()
		if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			return model.Err("backend_running", "a backend started during uninstall; retry after stopping it")
		}
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	report, e := install.Uninstall(a.home, *dry)
	if e != nil {
		return e
	}
	if e = a.print(report); e != nil {
		return e
	}
	if !report.Complete {
		return model.Err("uninstall_incomplete", "edited integrations were preserved; see report")
	}
	return nil
}

func (a *app) uninstallGuard(dry bool) error {
	if _, e := local.ReadEndpoint(a.home); e == nil {
		var status model.Status
		if e = a.call("status", nil, &status); e == nil {
			for _, l := range status.Leases {
				if l.State != "terminal" {
					return model.Err("active_leases", "finish or release outstanding leases before uninstalling")
				}
			}
			if !dry {
				quiet := *a
				quiet.out = io.Discard
				if _, e = quiet.daemon("daemon", []string{"stop"}); e != nil {
					return e
				}
			}
		}
	}
	path := filepath.Join(a.home, "state.db")
	if _, e := os.Stat(path); os.IsNotExist(e) {
		return nil
	}
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`PRAGMA busy_timeout=5000`); e != nil {
		return e
	}
	rows, e := db.Query(`SELECT data FROM leases`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var l model.Lease
		if e = rows.Scan(&b); e != nil {
			return e
		}
		if e = json.Unmarshal(b, &l); e != nil {
			return e
		}
		if l.State != "terminal" {
			return model.Err("active_leases", "outstanding leases remain in durable state")
		}
	}
	return rows.Err()
}
