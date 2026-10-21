package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type commandHelp struct {
	usage, summary, example string
	min, max                int
}

var commandHelps = map[string]commandHelp{
	"dashboard": {summary: "Open the embedded local web console for live monitoring, budgets, consumers, reasons, leases, messages and activity. A one-use link creates an eight-hour observation session; CLI controls remain available. Requires a running backend. Piped/JSON output never opens a browser.", example: "arm dashboard\n  arm dashboard --no-open\n  arm dashboard --json"},
	"monitor": {summary: "Show physical host CPU, memory/swap, filesystem space, per-device disk I/O and network rates with bounded history. --follow streams through the shared backend. Unknown counters and history gaps remain explicit; these are separate from lease budgets.", example: `arm monitor
  arm monitor --follow --json`},
	"agents":       {summary: "Read the full agent operating guide: resource leases, registration, warnings, polling, operator messages and safeguards. Works offline without creating state. Use arm agent list to list contacts.", example: `arm agents --json`},
	"start":        {summary: "Start the local backend in the background. Shortcut for arm daemon start; an existing backend keeps its budgets.", example: `arm start --cpu 4 --memory 8GiB --disk 20GiB`},
	"stop":         {summary: "Stop an idle backend. Shortcut for arm daemon stop; outstanding leases and unresolved obligations prevent shutdown."},
	"incidents":    {summary: "Show the latest 100 durable CPU/memory usage incidents, warning message IDs, evidence, deadlines and outcomes. Filter with --lease. Observed overage policy is opt-in for supervised run."},
	"usage":        {usage: "LEASE_ID [OPTIONS]", summary: "Observe CPU cores over an interval and platform-specific accounted memory for ARM-supervised live group members. Unknown or stale measurements remain explicit; observation is not a hard quota.", example: `arm usage LEASE_ID`, min: 1, max: 1},
	"pool create":  {usage: "NAME --scope host|global --capacity COUNT --reason TEXT [OPTIONS]", summary: "Create an agent-defined cooperative resource pool. Mutations return before/after state and a durable journal sequence.", example: `arm pool create browser-slots --scope host --capacity 3 --reason "Coordinate browsers"`, min: 1, max: 1},
	"pool list":    {summary: "List counted resources, host/global scope, exact capacities, consumers and queue. --all includes deleted definitions."},
	"pool inspect": {usage: "POOL_ID [OPTIONS]", summary: "Inspect a counted resource, revision, availability/deficit, consumers and queued reasons.", min: 1, max: 1},
	"pool history": {usage: "POOL_ID [OPTIONS]", summary: "Browse the transactional change journal: actor, attribution, time, reason and before/after state.", min: 1, max: 1},
	"pool set":     {usage: "POOL_ID COUNT --reason TEXT [OPTIONS]", summary: "Set nonnegative capacity using a revision guard. Reductions below reservations record a deficit without changing existing leases.", min: 2, max: 2},
	"pool adjust": {usage: "POOL_ID DELTA --reason TEXT [OPTIONS]", summary: "Atomically increase or reduce capacity by a signed integer. A repeated --key never applies the adjustment twice.", example: `arm pool adjust POOL_ID --reason "Free one more device" 1
  arm pool adjust POOL_ID -1 --reason "Device unavailable"`, min: 2, max: 2},
	"pool rename":     {usage: "POOL_ID NAME --reason TEXT [OPTIONS]", summary: "Rename a resource while preserving its ID, claims and history.", min: 2, max: 2},
	"pool describe":   {usage: "POOL_ID TEXT --reason TEXT [OPTIONS]", summary: "Change a pool description with an attributed journal entry.", min: 2, max: 2},
	"pool drain":      {usage: "POOL_ID --reason TEXT [OPTIONS]", summary: "Refuse new claims. Already queued requests continue under normal capacity and fairness rules.", min: 1, max: 1},
	"pool resume":     {usage: "POOL_ID --reason TEXT [OPTIONS]", summary: "Reopen a draining pool to new claims.", min: 1, max: 1},
	"pool delete":     {usage: "POOL_ID --reason TEXT [OPTIONS]", summary: "Preserve a historical tombstone. Queued or consuming claims prevent deletion.", min: 1, max: 1},
	"arm":             {usage: "[--home DIR] COMMAND [OPTIONS]", max: -1},
	"acquire":         {usage: "[OPTIONS]", summary: "Reserve CPU, memory, disk and/or ports without starting a command.", example: `arm acquire --cpu 2 --memory 1GiB --reason "Analysis workers"`},
	"run":             {usage: "[OPTIONS] -- COMMAND [ARG...]", summary: "Run a command under a supervised lease. ARM options precede COMMAND; everything after -- belongs to the command.", example: `arm run --cpu 2 --reason "Compile release binaries" -- cargo build --release`, min: 1, max: -1},
	"storage create":  {usage: "SIZE [OPTIONS]", summary: "Create a managed disk-only lease. SIZE accepts units such as 10GiB. --disk SIZE is an alternative.", example: `arm storage create 10GiB --reason "Retain build outputs"`, max: 1},
	"storage list":    {summary: "List managed storage leases and their retained obligations."},
	"storage inspect": {usage: "PATH [OPTIONS]", summary: "Measure logical and allocated storage without adopting or deleting it. Exclusive/reclaimable bytes may be unknown.", example: `arm storage inspect ./target`, min: 1, max: 1},
	"storage cleanup": {usage: "LEASE_ID [OPTIONS]", summary: "Reclaim verified managed storage after its consumers stop. --dry-run only previews. Unverified reclamation stays charged.", example: `arm storage cleanup LEASE_ID --dry-run`, min: 1, max: 1},
	"storage handoff": {usage: "LEASE_ID [OPTIONS]", summary: "Return files and unresolved storage responsibility to the operator. Preserves remaining data and ends ARM's cleanup authority.", example: `arm storage handoff LEASE_ID`, min: 1, max: 1},
	"ports reserve": {usage: "[PORT_SPEC...] [OPTIONS]", summary: "Hold port bindings. With no PORT_SPEC or --port, reserve one OS-selected TCP port on 127.0.0.1.", example: `arm ports reserve --reason "Preview server"
  arm ports reserve 8080 --reason "Preview server"
  arm ports reserve web=tcp6://[::1]:8000-8010 --reason "IPv6 preview"`, max: -1},
	"ports list":        {summary: "List ARM port reservations, consumers and reasons, including its protected backend listener."},
	"ports discover":    {summary: "Observe native TCP listeners and bound UDP sockets. Permission restrictions and unknown ownership remain explicit."},
	"inspect":           {usage: "LEASE_ID [OPTIONS]", summary: "Inspect one lease and its current outcome.", min: 1, max: 1},
	"release":           {usage: "LEASE_ID [OPTIONS]", summary: "End a lease early and begin its supported reclamation. Unresolved resources remain charged.", min: 1, max: 1},
	"cancel":            {usage: "LEASE_ID [OPTIONS]", summary: "Cancel queued or supervised work under this lease.", min: 1, max: 1},
	"reconcile":         {usage: "LEASE_ID [OPTIONS]", summary: "Verify that an uncertain process group or port allocation is gone. Never signals a recovered PID.", min: 1, max: 1},
	"renew":             {usage: "LEASE_ID --ttl DURATION [OPTIONS]", summary: "Extend a live lease when queue priority and parent lifetimes permit.", example: `arm renew LEASE_ID --ttl 20m`, min: 1, max: 1},
	"status":            {summary: "Show host budgets, leases and capabilities."},
	"resources":         {summary: "Show resource budgets, available capacity, consumers and reasons."},
	"messages":          {summary: "Show the operator inbox and message delivery status."},
	"events":            {summary: "Read durable events, or stream with --follow (JSON lines when redirected).", example: `arm events --follow`},
	"daemon start":      {summary: "Start the local backend in the background. An already running backend keeps its existing budgets.", example: `arm daemon start --cpu 4 --memory 8GiB --disk 20GiB`},
	"daemon stop":       {summary: "Stop an idle backend. Outstanding leases or unresolved resource obligations prevent shutdown."},
	"daemon status":     {summary: "Show the running backend's status and budgets."},
	"serve":             {summary: "Run the local backend in the foreground until interrupted.", example: `arm serve --cpu 4 --memory 8GiB --disk 20GiB`},
	"agent probe":       {summary: "Inspect installed agent CLI versions and messaging capabilities."},
	"agent register":    {summary: "Register one exact supported agent session for attribution and messages.", example: `arm agent register --id builder --adapter codex --session SESSION_UUID`},
	"agent list":        {summary: "List registered agent contacts."},
	"agent inbox":       {summary: "Read messages for an exact registered session. Reading never acknowledges them. Returns a page with an opaque resume cursor.", example: `arm agent inbox --id builder --session SESSION_UUID --pending`},
	"agent watch":       {summary: "Watch an exact recipient inbox (JSON lines when redirected or with --json). Reconnect with the last emitted cursor; use --once for one bounded batch. Output never acknowledges messages.", example: `arm agent watch --id builder --session SESSION_UUID`},
	"agent receive":     {summary: "Explicitly record that a registered session received a message. This is separate from acknowledgement.", example: `arm agent receive --id builder --session SESSION_UUID --message MESSAGE_ID`},
	"agent ack":         {summary: "Explicitly acknowledge a message. Optional response text reaches the operator inbox. An acknowledgement does not prove resource compliance.", example: `arm agent ack --id builder --session SESSION_UUID --message MESSAGE_ID --text "Reduced build concurrency"`},
	"message operator":  {summary: "Send a durable message to the operator inbox.", example: `arm message operator --text "The build needs another five minutes."`},
	"message send":      {summary: "Send a message to one registered agent contact.", example: `arm message send --to builder --text "Please reduce build concurrency."`},
	"message broadcast": {summary: "Send a message to every currently registered contact.", example: `arm message broadcast --text "Please release idle leases."`},
	"install":           {summary: "Install verified managed agent instructions. Preview with --dry-run. Backend startup is separate."},
	"uninstall":         {summary: "Remove unchanged instructions using the saved installation manifest, preserving edits and resource obligations. The executable and state remain."},
	"version":           {summary: "Print ARM, Go and platform versions."},
}
var groupHelps = map[string]string{
	"pool":    "  create NAME --scope SCOPE --capacity COUNT  Create a host/global pool\n  list|inspect ID|history ID                  Browse definitions, claims and journal\n  set ID COUNT|adjust ID DELTA                Set capacity or apply a signed delta\n  rename ID NAME|describe ID TEXT             Update resource metadata\n  drain ID|resume ID                          Close or reopen new admission\n  delete ID                                  Tombstone a pool with no outstanding claims\n",
	"storage": "  create SIZE       Create a managed disk lease\n  list              List storage leases\n  inspect PATH      Measure a directory without changing it\n  cleanup LEASE_ID  Reclaim managed storage (--dry-run previews)\n  handoff LEASE_ID  Preserve remaining files and return responsibility\n",
	"ports":   "  reserve [PORT_SPEC...]  Hold bindings (default: TCP loopback, OS-selected port)\n  list                    List ARM allocations, consumers and reasons\n  discover                Observe native bindings and visible owners\n",
	"daemon":  "  start   Start the backend in the background\n  stop    Stop an idle backend\n  status  Inspect backend status\n",
	"agent":   "  probe     Inspect installed CLIs and messaging capabilities\n  register  Register an exact session (--delivery poll for an inbox)\n  list      List contacts\n  inbox     Read a recipient's durable messages\n  watch     Stream inbox messages with reconnect\n  receive   Record receipt of one message\n  ack       Acknowledge one message and optionally reply\n",
	"message": "  operator   Send to the operator inbox\n  send       Send to one contact (--to ID)\n  broadcast  Send to all registered contacts\n",
}

func (a *app) group(name string, args []string) bool {
	if len(args) != 0 && !(len(args) == 1 && (args[0] == "--help" || args[0] == "-help" || args[0] == "-h")) {
		return false
	}
	text, ok := groupHelps[name]
	if !ok {
		return false
	}
	fmt.Fprintf(a.out, "Usage: arm %s COMMAND [OPTIONS]\n\nCommands:\n%s\nUse arm %s COMMAND --help for options.\n", name, text, name)
	return true
}
func (a *app) usage(f *flag.FlagSet) {
	if f.Name() == "arm" {
		a.helpTo(f.Output())
		return
	}
	h := commandHelps[f.Name()]
	fmt.Fprintf(f.Output(), "%s arm %s", a.paint("1;36", "Usage:"), f.Name())
	if h.usage != "" {
		fmt.Fprint(f.Output(), " ", h.usage)
	} else {
		fmt.Fprint(f.Output(), " [OPTIONS]")
	}
	fmt.Fprintln(f.Output())
	if h.summary != "" {
		fmt.Fprintln(f.Output(), "\n"+h.summary)
	}
	if f.NFlag() > 0 || hasFlags(f) {
		fmt.Fprintln(f.Output(), "\nOptions:")
	}
	f.VisitAll(func(v *flag.Flag) {
		name, usage := flag.UnquoteUsage(v)
		if v.Name == "json" {
			usage = "force JSON output (default: automatic terminal detection)"
		}
		isBool := false
		if b, ok := v.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = b.IsBoolFlag()
		}
		label := "--" + v.Name
		if !isBool {
			if name == "string" || name == "value" {
				name = "VALUE"
			}
			label += " " + strings.ToUpper(name)
		}
		fmt.Fprintf(f.Output(), "  %-30s %s", label, usage)
		if v.DefValue != "" && v.DefValue != "0" && v.DefValue != "0s" && v.DefValue != "false" && v.DefValue != "[]" {
			if isBool && v.DefValue == "true" {
				fmt.Fprint(f.Output(), " (default)")
			} else {
				fmt.Fprintf(f.Output(), " (default %s)", v.DefValue)
			}
		}
		fmt.Fprintln(f.Output())
	})
	if h.example != "" {
		fmt.Fprintln(f.Output(), "\nExamples:\n  "+h.example)
	}
}
func hasFlags(f *flag.FlagSet) bool {
	found := false
	f.VisitAll(func(*flag.Flag) { found = true })
	return found
}

// parse permits flags around positional arguments, except root commands and
// run: once their command begins, the remaining arguments belong to it.
type argumentError struct {
	err     error
	command string
}

func (e *argumentError) Error() string {
	command := "arm " + e.command
	if e.command == "arm" {
		command = "arm"
	}
	return fmt.Sprintf("%v; see %s --help", e.err, command)
}
func (e *argumentError) Unwrap() error { return e.err }
func parseQuiet(f *flag.FlagSet, args []string) error {
	out := f.Output()
	f.SetOutput(io.Discard)
	err := f.Parse(args)
	f.SetOutput(out)
	return err
}
func (a *app) parse(f *flag.FlagSet, args []string, stopAtCommand bool) (err error) {
	a.commandName = f.Name()
	defer func() {
		f.Visit(func(v *flag.Flag) {
			if v.Name == "json" {
				a.forceJSON = v.Value.String() == "true"
			}
		})
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			err = &argumentError{err, f.Name()}
		}
	}()
	options, positionals := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "--help" || arg == "-help" || arg == "-h" {
			if err := parseQuiet(f, options); err != nil {
				return err
			}
			out := f.Output()
			f.SetOutput(a.out)
			err := f.Parse([]string{"--help"})
			f.SetOutput(out)
			return err
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if stopAtCommand {
				positionals = append(positionals, args[i:]...)
				break
			}
			positionals = append(positionals, arg)
			continue
		}
		if f.Name() == "pool adjust" {
			if _, err := strconv.ParseInt(arg, 10, 64); err == nil {
				positionals = append(positionals, arg)
				continue
			}
		}
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		v := f.Lookup(name)
		if v == nil {
			return parseQuiet(f, append(options, arg))
		}
		options = append(options, arg)
		isBool := false
		if b, ok := v.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = b.IsBoolFlag()
		}
		if !inline && !isBool {
			if i+1 == len(args) || args[i+1] == "--" {
				return fmt.Errorf("--%s requires a value", name)
			}
			nextName, _, _ := strings.Cut(strings.TrimLeft(args[i+1], "-"), "=")
			if strings.HasPrefix(args[i+1], "-") && (f.Lookup(nextName) != nil || nextName == "help" || nextName == "h") {
				return fmt.Errorf("--%s requires a value before %s", name, args[i+1])
			}
			i++
			options = append(options, args[i])
		}
	}
	if err := parseQuiet(f, append(append(options, "--"), positionals...)); err != nil {
		return err
	}
	if h, ok := commandHelps[f.Name()]; ok {
		n := len(f.Args())
		if n < h.min || (h.max >= 0 && n > h.max) {
			return fmt.Errorf("expected usage: arm %s %s", f.Name(), h.usage)
		}
	}
	return nil
}

func (a *app) helpTo(out io.Writer) { original := a.out; a.out = out; a.help(); a.out = original }
