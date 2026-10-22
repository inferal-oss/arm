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

	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/install"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/server"
)

const Version = "0.1.0-dev"

type app struct {
	home                          string
	in                            io.Reader
	out, err                      io.Writer
	ctx                           context.Context
	outputTTY, errorTTY, inputTTY bool
	forceJSON, colors             bool
	commandName                   string
	showDiff, diffShown           bool
}

func Main(args []string, in io.Reader, out, errout io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := app{home: local.Home(), in: in, out: out, err: errout, ctx: ctx}
	a.outputTTY, a.errorTTY, a.inputTTY = terminal(out), terminal(errout), terminal(in)
	a.colors = os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	f := a.flags("arm")
	f.StringVar(&a.home, "home", a.home, "private ARM state directory (or ARM_HOME)")
	f.BoolVar(&a.forceJSON, "json", false, "force JSON output, including in a terminal")
	if e := a.parse(f, args, true); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		a.reportError(e)
		return 2
	}
	abs, e := filepath.Abs(a.home)
	if e != nil {
		a.reportError(e)
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
		var detail *model.Error
		if errors.As(e, &detail) && (detail.Code == "agent_message_outcome_unknown" || detail.Code == "budget_outcome_unknown" || detail.Code == "shutdown_wait_interrupted" || detail.Code == "remote_outcome_unknown" || detail.Code == "remote_wait_interrupted" || detail.Code == "mesh_connect_incomplete" || detail.Code == "mesh_disconnect_incomplete") {
			a.reportError(detail)
		} else {
			a.reportError(model.Err("interrupted", "operation interrupted"))
		}
		return 130
	}
	if errors.Is(e, flag.ErrHelp) {
		return 0
	}
	if e != nil {
		var argument *argumentError
		if errors.As(e, &argument) {
			code = 2
		}
		a.reportError(e)
		if code == 0 {
			code = 1
		}
	}
	return code
}
func (a *app) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.err)
	f.Usage = func() { a.usage(f) }
	return f
}
func (a *app) print(v any) error {
	if !a.machine() {
		return a.human(v)
	}
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
	fmt.Fprintln(a.out, a.paint("1;36", "Agent Resource Manager")+`
Usage: arm [--home DIR] COMMAND [OPTIONS]

Commands:
  start|stop                Start or stop the local backend
  daemon start|stop|status    Manage the local backend
  serve                     Run the backend in the foreground
  status                    Show host budgets and leases
  usage LEASE_ID            Show supervised CPU/memory observations
  incidents                 Show durable usage incidents and warnings
  resources                 Show budgets, availability, owners, and reasons
  monitor                   Observe physical host usage and short history
  dashboard                 Open the authenticated local web console
  mesh                      Connect, inspect and disconnect trusted hosts
  storage COMMAND           Manage files, exports and short retention
  ports reserve|list|discover Manage port reservations and native inventory
  pool create|list|inspect|history|set|adjust  Manage counted host/global pools
  budget list|inspect|create|set|history      Manage local owner policy ceilings
  acquire                   Request a resource lease
  run                       Execute a command under a supervised lease
  inspect|release|cancel ID  Inspect or end a lease
  reconcile ID              Verify a recovered process group has stopped
  renew ID --ttl DURATION    Extend a lease when the queue permits
  events [--follow]          Read or stream durable events
  agents                    Read the offline guide and list knowledge cards
  agents card NAME          Read a workload knowledge card
  agent probe|register|list  Discover CLIs and register exact sessions
  agent inbox|watch|receive|ack  Poll notifications and acknowledge explicitly
  message operator|send|broadcast  Send a durable message
  messages                  Read the operator inbox and delivery status
  install|uninstall         Manage reversible agent instruction blocks
  version                   Print build and Go versions

Use COMMAND --help for options. Terminal output is human-readable; pipes use JSON.
Use --json to force JSON output, including in a terminal.
Use arm help COMMAND or arm GROUP --help to browse commands.
Global --home DIR goes before COMMAND; ARM_HOME selects the same state directory.
This initial backend uses cooperative CPU/memory/disk budgets, not OS quotas.`)
}

func (a *app) command(args []string) (int, error) {
	cmd, args := args[0], args[1:]
	if a.group(cmd, args) {
		return 0, nil
	}
	switch cmd {
	case "help":
		if len(args) > 0 {
			name := strings.Join(args, " ")
			if name == "arm" {
				a.help()
				return 0, nil
			}
			if _, ok := groupHelps[name]; ok {
				a.group(name, nil)
				return 0, nil
			}
			if _, ok := commandHelps[name]; !ok {
				return 2, fmt.Errorf("unknown help topic %q", name)
			}
			return a.command(append(args, "--help"))
		}
		a.help()
		return 0, nil
	case "version":
		f := a.flags("version")
		f.Bool("json", false, "force JSON output (default: automatic)")
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		return 0, a.print(map[string]string{"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH})
	case "serve", "daemon", "start", "stop":
		return a.daemon(cmd, args)
	case "acquire", "run":
		return a.acquire(cmd, args)
	case "install", "uninstall":
		return 0, a.integration(cmd, args)
	case "agents":
		return 0, a.agentsGuide(args)
	case "mesh":
		return a.meshCommand(args)
	case "monitor":
		return 0, a.monitorCommand(args)
	case "dashboard":
		return 0, a.dashboard(args)
	case "agent":
		err := a.agent(args)
		if errors.Is(err, errAgentDeliveryReported) {
			return 1, nil
		}
		return 0, err
	case "message":
		return 0, a.message(args)
	case "storage":
		return a.storage(args)
	case "ports":
		return a.ports(args)
	case "pool":
		return a.pool(args)
	case "budget":
		return a.budget(args)
	case "events":
		f := a.flags(cmd)
		follow := f.Bool("follow", false, "stream live events")
		after := f.Int64("after", 0, "resume after this event sequence")
		f.Bool("json", false, "force JSON output (default: automatic)")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if *follow {
			return 0, client.Follow(a.ctx, a.home, *after, func(ev model.Event) error {
				if !a.machine() {
					return a.print(ev)
				}
				return json.NewEncoder(a.out).Encode(ev)
			})
		}
		var result any
		if e := a.call("events", map[string]any{"after": *after}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "status", "resources", "messages":
		f := a.flags(cmd)
		f.Bool("json", false, "JSON output")
		host := f.String("host", "localhost", "target host ID or unique peer name")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		var result any
		if e := a.hostCall(*host, cmd, nil, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "incidents":
		f := a.flags("incidents")
		lease := f.String("lease", "", "filter by lease `ID`")
		f.Bool("json", false, "JSON incident records and evidence")
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		var result []model.UsageIncident
		if err := a.call("incidents", map[string]string{"lease": *lease}, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	case "usage":
		f := a.flags("usage")
		f.Bool("json", false, "JSON output with observation scope and uncertainty")
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		var result model.UsageView
		if err := a.call("usage", map[string]string{"id": f.Args()[0]}, &result); err != nil {
			return 1, err
		}
		return 0, a.print(result)
	case "inspect", "release", "cancel", "reconcile", "renew":
		f := a.flags(cmd)
		host := f.String("host", "localhost", "target host ID or unique peer name")
		all, key := new(bool), new(string)
		if cmd == "release" || cmd == "cancel" {
			f.BoolVar(all, "all", false, "end all outstanding leases at this local authority")
			f.StringVar(key, "key", "", "bulk or remote operation `key` for exact retries")
		}
		ttl := new(time.Duration)
		if cmd == "renew" {
			f.StringVar(key, "key", "", "remote operation key for exact retries")
			f.DurationVar(ttl, "ttl", 0, "required renewal `duration` from now")
		}
		f.Bool("json", false, "force JSON output (default: automatic)")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if *host != "localhost" {
			var status model.Status
			if e := a.call("status", nil, &status); e != nil {
				return 1, e
			}
			if *host == status.Host {
				*host = "localhost"
			}
		}
		if cmd == "release" || cmd == "cancel" {
			if *all {
				if *host != "localhost" {
					return 2, model.Err("unsupported_remote_operation", "remote bulk actions are not enabled")
				}
				if len(f.Args()) != 0 {
					return 2, fmt.Errorf("--all cannot be combined with a lease ID")
				}
				return a.bulkEnd(cmd, *key)
			}
			if len(f.Args()) != 1 || (*key != "" && *host == "localhost") {
				return 2, fmt.Errorf("provide one lease ID or --all; --key requires --all or a remote host")
			}
		}
		id := f.Args()[0]
		if cmd == "renew" && *ttl <= 0 {
			return 2, fmt.Errorf("renew requires --ttl DURATION greater than zero")
		}
		if *host != "localhost" && mesh.Mutating(cmd) && *key == "" {
			*key = local.ID()
		}
		if *host == "localhost" && cmd == "renew" && *key != "" {
			return 2, fmt.Errorf("renew --key applies only to remote operations")
		}
		var result model.Lease
		if e := a.hostCallKey(*host, cmd, *key, map[string]any{"id": id, "ttl_ns": *ttl}, &result); e != nil {
			return 1, e
		}
		if *host != "localhost" && mesh.Mutating(cmd) {
			raw, _ := json.Marshal(result)
			var view map[string]any
			_ = json.Unmarshal(raw, &view)
			view["operation_key"] = *key
			view["target_host"] = result.Host
			return 0, a.print(view)
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
	label := command
	if command == "storage" {
		label = "storage create"
	}
	if command == "ports" {
		label = "ports reserve"
	}
	f := a.flags(label)
	defaultCPU := float64(1)
	if command == "storage" || command == "ports" {
		defaultCPU = 0
	}
	cpu := &defaultCPU
	memoryValue, diskValue := "0", "0"
	memory, disk := &memoryValue, &diskValue
	if command == "acquire" || command == "run" {
		f.Float64Var(cpu, "cpu", defaultCPU, "logical-core `budget` (cooperative)")
		f.StringVar(memory, "memory", "0", "memory `size`, e.g. 2GiB")
	}
	if command != "ports" {
		f.StringVar(disk, "disk", "0", "disk `size`, e.g. 10GiB")
	}
	reason := f.String("reason", "", "required purpose")
	ttl := f.Duration("ttl", 10*time.Minute, "lease duration from grant")
	budget := f.String("budget", "", "local policy budget ID (default: authenticated owner root); additional allocation, not parent-reservation reuse")
	startDeadline := f.String("start-deadline", "", "latest start `timestamp` (absolute RFC3339, local host only)")
	wait := f.Bool("wait", false, "queue and wait for capacity")
	key := f.String("key", "", "idempotency key")
	host := f.String("host", "localhost", "target host")
	profile := new(string)
	project := f.String("project", "", "project attribution")
	agent := f.String("agent", "", "registered agent contact for attribution")
	storageLease, portLease, cwd := new(string), new(string), new(string)
	serviceLease, detach := new(string), new(bool)
	var usagePolicy model.UsagePolicy
	if command == "run" {
		f.StringVar(&usagePolicy.Mode, "usage-policy", "", "opt-in observed overage policy: warn or revoke (requires --agent)")
		f.DurationVar(&usagePolicy.Window, "usage-window", 5*time.Second, "sustained observed breach `duration` before warning")
		f.DurationVar(&usagePolicy.Grace, "usage-grace", 30*time.Second, "remediation `duration` after each warning")
		f.IntVar(&usagePolicy.Warnings, "usage-warnings", 3, "maximum warning `count` per incident")
		f.Float64Var(&usagePolicy.Tolerance, "usage-tolerance", .1, "allowed fraction above budget (0..1)")
		f.StringVar(profile, "profile", "", "workload `profile` (rust-build, gradle-service or gradle-build)")
		f.StringVar(storageLease, "storage", "", "attach managed storage `lease` (must outlast this command)")
		f.StringVar(serviceLease, "service", "", "attach a verified ready Gradle service `lease` (gradle-build only)")
		f.BoolVar(detach, "detach", false, "return the initial supervised service lease (gradle-service only; inspect readiness)")
		f.StringVar(portLease, "port-lease", "", "attach a dedicated port `lease` (one consumer)")
		f.StringVar(cwd, "cwd", "", "command working `directory` (default: current directory)")
	}
	var poolClaims poolFlags
	if command == "acquire" || command == "run" {
		f.Var(&poolClaims, "pool", "repeatable counted claim: POOL_ID=COUNT")
	}
	var portFlags portFlags
	portMode := new(string)
	dualStack := new(bool)
	if command != "storage" {
		f.Var(&portFlags, "port", "repeatable port `spec`: [NAME=]tcp4|tcp6|udp4|udp6://IP:PORT[-END]")
		modes := "hold (default) or cooperative"
		if command == "run" {
			modes = "activation (default) or cooperative"
		}
		f.StringVar(portMode, "port-mode", "", modes)
		f.BoolVar(dualStack, "port-dual-stack", false, "IPv6 wildcard bindings also cover IPv4")
	}
	asJSON := new(bool)
	if command == "run" {
		f.BoolVar(asJSON, "json", false, "send command output to stderr and final lease JSON to stdout")
	} else {
		f.BoolVar(asJSON, "json", false, "return the lease as JSON")
	}
	if e := a.parse(f, args, command == "run"); e != nil {
		return 2, e
	}
	if command == "run" {
		*asJSON = a.machine()
		if *detach && *profile != "gradle-service" {
			return 2, fmt.Errorf("--detach requires --profile gradle-service; ordinary run follows command completion")
		}
	}
	if command == "storage" && len(f.Args()) > 0 {
		providedDisk := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "disk" {
				providedDisk = true
			}
		})
		if providedDisk {
			return 2, fmt.Errorf("provide storage SIZE as an argument or --disk SIZE, not both")
		}
		*disk = f.Args()[0]
	}
	if command == "storage" && *disk == "0" {
		return 2, fmt.Errorf("storage create requires SIZE; for example: arm storage create 10GiB --reason \"Retain build outputs\"")
	}
	if command == "ports" {
		if len(f.Args()) > 0 && len(portFlags) > 0 {
			return 2, fmt.Errorf("provide port specs as arguments or --port flags, not both")
		}
		for _, spec := range f.Args() {
			if !strings.Contains(spec, "://") {
				spec = "tcp4://127.0.0.1:" + spec
			}
			if e := portFlags.Set(spec); e != nil {
				return 2, e
			}
		}
		if len(portFlags) == 0 {
			portFlags = append(portFlags, model.PortClaim{Network: "tcp4", Address: "127.0.0.1"})
		}
	}
	if strings.TrimSpace(*reason) == "" {
		return 2, fmt.Errorf("--reason is required; see arm %s --help", label)
	}
	if command == "acquire" && len(poolClaims) > 0 {
		explicitCPU := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "cpu" {
				explicitCPU = true
			}
		})
		if !explicitCPU {
			*cpu = 0
		}
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
	var deadline *time.Time
	if *startDeadline != "" {
		stamp, parseErr := time.Parse(time.RFC3339Nano, *startDeadline)
		if parseErr != nil {
			return 2, fmt.Errorf("--start-deadline requires an absolute RFC3339 timestamp: %w", parseErr)
		}
		stamp = stamp.UTC()
		deadline = &stamp
	}
	r := model.Request{Budget: *budget, StartDeadline: deadline, Host: *host, Reason: *reason, Project: *project, Agent: *agent, Resources: model.Resources{CPU: *cpu, Memory: mem, Disk: d}, TTL: *ttl, Key: *key, Queue: *wait, Profile: *profile}
	if usagePolicy.Mode != "" {
		r.UsagePolicy = &usagePolicy
	} else {
		orphan := false
		f.Visit(func(v *flag.Flag) {
			if strings.HasPrefix(v.Name, "usage-") {
				orphan = true
			}
		})
		if orphan {
			return 2, fmt.Errorf("usage options require --usage-policy warn or revoke")
		}
	}
	r.Pools = poolClaims
	r.StorageLease = *storageLease
	r.ServiceLease = *serviceLease
	r.PortLease = *portLease
	for _, c := range portFlags {
		c.Mode = *portMode
		c.DualStack = *dualStack
		r.Ports = append(r.Ports, c)
	}
	if len(portFlags) == 0 && (*portMode != "" || *dualStack) {
		return 2, fmt.Errorf("port-mode and port-dual-stack require at least one --port")
	}
	if command == "storage" {
		r.Kind = "storage"
	}
	if command == "ports" {
		r.Kind = "ports"
	}
	defer func() {
		if a.ctx.Err() != nil && *host == "localhost" {
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
		if *host != "localhost" && (*cwd == "" || !filepath.IsAbs(*cwd)) {
			return 2, fmt.Errorf("remote run requires explicit absolute --cwd on the target host; files are not uploaded")
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
		if *host == "localhost" {
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
		}
		e = a.hostCall(*host, "run", manager.RunRequest{Request: r, Command: argv, CWD: *cwd}, &l)
		if e != nil {
			return 1, e
		}
	} else {
		if e = a.hostCall(*host, "acquire", r, &l); e != nil {
			return 1, e
		}
	}
	if *detach {
		if l.State != "active" || l.Workload == nil {
			if err := a.print(l); err != nil {
				return 1, err
			}
			return 1, model.Err("service_not_started", "service lease "+l.ID+" did not start: "+l.Failure)
		}
		return a.acquired(l)
	}
	if command != "run" && (!*wait || l.State != "queued") {
		return a.acquired(l)
	}
	var offset int64
	for {
		if l.QueueInfo != nil && l.QueueInfo.Rejection != nil {
			return a.acquired(l)
		}
		if a.ctx.Err() != nil {
			if *host != "localhost" {
				return 130, model.Err("remote_wait_interrupted", fmt.Sprintf("remote lease %s on %s may continue; inspect its outcome before retrying exact key %q", l.ID, *host, r.Key))
			}
			return 130, a.ctx.Err()
		}
		if command == "run" {
			var b []byte
			if e = a.hostCall(*host, "output", map[string]any{"id": l.ID, "offset": offset}, &b); e != nil {
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
		if e = a.hostCall(*host, "inspect", map[string]string{"id": l.ID}, &l); e != nil {
			return 1, e
		}
		if l.QueueInfo != nil && l.QueueInfo.Rejection != nil {
			return a.acquired(l)
		}
		if command != "run" && l.State != "queued" {
			return a.acquired(l)
		}
		if l.State == "terminal" {
			if command == "run" {
				var b []byte
				if e = a.hostCall(*host, "output", map[string]any{"id": l.ID, "offset": offset}, &b); e != nil {
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
			if e = a.print(l); e != nil {
				return 1, e
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
			if e = a.print(l); e != nil {
				return 1, e
			}
			return 1, model.Err("needs_attention", "lease "+l.ID+" requires operator reconciliation: "+l.Failure)
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
	if l.QueueInfo != nil && l.QueueInfo.Rejection != nil {
		return 1, nil
	}
	if l.State == "needs_attention" {
		return 1, model.Err("needs_attention", "allocation remains charged; inspect its failure and ownership details")
	}
	return 0, nil
}

func (a *app) daemon(command string, args []string) (int, error) {
	action := "serve"
	if command == "start" || command == "stop" {
		action = command
	}
	if command == "daemon" {
		if len(args) == 0 {
			return 2, fmt.Errorf("daemon requires start, stop, or status")
		}
		action, args = args[0], args[1:]
	}
	if action != "start" && action != "stop" && action != "status" && !(command == "serve" && action == "serve") {
		return 2, fmt.Errorf("unknown daemon action %q; use arm daemon --help", action)
	}
	label := "daemon " + action
	if command != "daemon" {
		label = command
	}
	f := a.flags(label)
	cpuValue := float64(max(1, runtime.NumCPU()-1))
	memValue, diskValue, listenValue := "auto", "auto", "127.0.0.1:0"
	cpus, mem, disk, listen := &cpuValue, &memValue, &diskValue, &listenValue
	if action == "start" || action == "serve" {
		f.Float64Var(cpus, "cpu", cpuValue, "host CPU reservation `budget`")
		f.StringVar(mem, "memory", memValue, "live host memory pool: auto, or a positive optional ceiling `size`")
		f.StringVar(disk, "disk", diskValue, "live state-filesystem pool: auto, or a positive optional ceiling `size`")
		f.StringVar(listen, "listen", listenValue, "loopback backend `address`")
	}
	if action != "serve" {
		f.Bool("json", false, "force JSON output (default: automatic)")
	}
	force, wait, timeout := new(bool), new(bool), new(int64)
	if action == "stop" {
		f.BoolVar(force, "force", false, "release leases now using normal provider safeguards")
		f.BoolVar(wait, "wait", false, "drain existing work; cancel queued requests and pause admission/renewal")
		f.Int64Var(timeout, "timeout", 0, "force release after this many positive `seconds` (requires --wait)")
	}
	if e := a.parse(f, args, false); e != nil {
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
		timeoutSet := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "timeout" {
				timeoutSet = true
			}
		})
		if *force && *wait || timeoutSet && (!*wait || *timeout <= 0) || *timeout > int64((1<<63-1)/int64(time.Second)) {
			return 2, fmt.Errorf("--force and --wait are exclusive; --timeout requires --wait and positive seconds within duration range")
		}
		return a.stopBackend(model.ShutdownRequest{Force: *force, Wait: *wait, Timeout: time.Duration(*timeout) * time.Second})
	}
	var memory int64
	var e error
	if *mem != "auto" {
		memory, e = bytesValue(*mem)
		if e != nil || memory <= 0 {
			return 2, fmt.Errorf("--memory must be auto or a positive ceiling size")
		}
	}
	var d int64
	if *disk != "auto" {
		d, e = bytesValue(*disk)
		if e != nil || d <= 0 {
			return 2, fmt.Errorf("--disk must be auto or a positive ceiling size")
		}
	}
	if *cpus <= 0 || math.IsNaN(*cpus) || math.IsInf(*cpus, 0) || d < 0 {
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
	memoryArg := *mem
	if memory > 0 {
		memoryArg = strconv.FormatInt(memory, 10)
	}
	diskArg := *disk
	if d > 0 {
		diskArg = strconv.FormatInt(d, 10)
	}
	cmd := exec.Command(exe, "--home", a.home, "serve", "--cpu", strconv.FormatFloat(*cpus, 'f', -1, 64), "--memory", memoryArg, "--disk", diskArg, "--listen", *listen)
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
		return fmt.Errorf("agent requires probe, register, list, send, reply, message-lookup, inbox, watch, receive, or ack")
	}
	action, args := args[0], args[1:]
	if action == "send" || action == "reply" || action == "message-lookup" {
		return a.agentMessage(action, args)
	}
	if action == "inbox" || action == "watch" || action == "receive" || action == "ack" {
		return a.agentInbox(action, args)
	}
	f := a.flags("agent " + action)
	var c agents.Contact
	if action != "probe" && action != "register" && action != "list" {
		return fmt.Errorf("unknown agent action %q; use arm agent --help", action)
	}
	if action == "probe" || action == "register" {
		f.StringVar(&c.Adapter, "adapter", "", "codex, claude, or generic (poll only)")
	}
	if action == "register" {
		f.StringVar(&c.Delivery, "delivery", "native", "native CLI or poll inbox")
		f.StringVar(&c.ID, "id", "", "stable contact `name`")
		f.StringVar(&c.Session, "session", "", "exact existing session `UUID`")
		f.StringVar(&c.CWD, "cwd", "", "session working `directory` (default: current directory)")
		f.StringVar(&c.ConfigDir, "config-dir", "", "agent configuration `directory`")
		f.StringVar(&c.Remote, "remote", "", "Codex unix `endpoint` for an isolated server")
		f.BoolVar(&c.Idle, "idle", false, "declare that the Claude session is idle")
	}
	f.Bool("json", false, "force JSON output (default: automatic)")
	if e := a.parse(f, args, false); e != nil {
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
	if c.Adapter == "generic" && c.Session == "" {
		c.Session = uuid.NewString()
	}
	if c.CWD == "" {
		var e error
		c.CWD, e = os.Getwd()
		if e != nil {
			return e
		}
	}
	var pathErr error
	c.CWD, pathErr = filepath.Abs(c.CWD)
	if pathErr != nil {
		return pathErr
	}
	if c.ConfigDir != "" {
		c.ConfigDir, pathErr = filepath.Abs(c.ConfigDir)
		if pathErr != nil {
			return pathErr
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
	if action != "operator" && action != "send" && action != "broadcast" {
		return fmt.Errorf("unknown message action %q; use arm message --help", action)
	}
	to := new(string)
	key, replyTo := new(string), new(string)
	if action == "send" {
		f.StringVar(to, "to", "", "required registered contact `ID`")
		f.StringVar(replyTo, "reply-to", "", "operator inbox message being answered")
	}
	if action != "operator" {
		f.StringVar(key, "key", "", "idempotency key for an exact send retry (generated when omitted)")
	}
	body := f.String("text", "", "message body (or - for stdin)")
	lease := f.String("lease", "", "related lease ID")
	f.Bool("json", false, "force JSON output (default: automatic)")
	if e := a.parse(f, args, false); e != nil {
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
	if *body == "" || len(*body) > 16384 || len(*key) > 128 {
		return fmt.Errorf("message requires 1 to 16384 body bytes and a key of at most 128 bytes")
	}
	if action == "operator" {
		if err := a.call("message", map[string]string{"audience": audience, "body": *body, "lease": *lease}, &out); err != nil {
			return err
		}
	} else {
		var contacts []agents.Contact
		if err := a.call("agents", nil, &contacts); err != nil {
			return err
		}
		targets := make([]agents.Contact, 0)
		for _, contact := range contacts {
			if action == "broadcast" || contact.ID == audience {
				targets = append(targets, contact)
			}
		}
		if *key == "" {
			*key = local.ID()
		}
		request := manager.MessageRequest{Key: *key, Audience: audience, Body: *body, Lease: *lease, ReplyTo: *replyTo, Targets: targets}
		if err := a.call("message_send", request, &out); err != nil {
			var api *model.Error
			if errors.As(err, &api) {
				return model.Err(api.Code, fmt.Sprintf("%s (message operation key %q)", api.Message, *key))
			}
			return fmt.Errorf("message operation key %q; check delivery reports and retry the exact request with this --key if needed: %w", *key, err)
		}
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
	yes := f.Bool("yes", false, "apply without prompting (required for scripts/agents)")
	f.BoolVar(&a.showDiff, "diff", false, "show unified file diffs in a terminal (JSON always includes them)")
	codexRoot, claudeRoot := new(string), new(string)
	if action == "install" {
		f.StringVar(codexRoot, "codex-home", "", "explicit Codex integration `directory`")
		f.StringVar(claudeRoot, "claude-home", "", "explicit Claude integration `directory`")
	}
	f.Bool("json", false, "force JSON output (default: automatic)")
	if e := a.parse(f, args, false); e != nil {
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
		plan, e := install.PreviewInstall(a.home, targets)
		if e != nil {
			return e
		}
		if *dry {
			return a.print(plan.Report)
		}
		if e = a.approve(plan, *yes); e != nil {
			return e
		}
		report, e := plan.Apply()
		if e != nil {
			if report.BackupDirectory != "" {
				report.Complete = false
				if outErr := a.print(report); outErr != nil {
					return outErr
				}
			}
			return e
		}
		return a.print(report)
	}
	if e := a.uninstallGuard(true); e != nil {
		return e
	}
	plan, e := install.PreviewUninstall(a.home)
	if e != nil {
		return e
	}
	plan.Report.Actions = append(plan.Report.Actions, install.Action{Path: a.home, Action: "stop-idle-backend", Detail: "If running, stop the idle backend after rechecking leases; retain state and history"})
	if *dry {
		if e = a.print(plan.Report); e != nil {
			return e
		}
		if !plan.Report.Complete {
			return model.Err("uninstall_incomplete", "edited integrations were preserved; see report")
		}
		return nil
	}
	if e = a.approve(plan, *yes); e != nil {
		return e
	}
	if e = a.uninstallGuard(false); e != nil {
		return e
	}
	if _, e = os.Stat(a.home); os.IsNotExist(e) {
		report := plan.Report
		report.DryRun = false
		report.Actions[len(report.Actions)-1] = install.Action{Path: a.home, Action: "backend-idle", Detail: "Backend is stopped; no state directory exists"}
		return a.print(report)
	} else if e != nil {
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
	report, e := plan.Apply()
	if e != nil {
		if report.BackupDirectory != "" {
			report.Complete = false
			if outErr := a.print(report); outErr != nil {
				return outErr
			}
		}
		return e
	}
	report.Actions = append(report.Actions, install.Action{Path: a.home, Action: "backend-idle", Detail: "Backend is stopped; state and history are retained"})
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
