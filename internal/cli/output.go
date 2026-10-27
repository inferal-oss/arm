package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"getarm.org/internal/install"
	"getarm.org/internal/model"
	"github.com/mattn/go-isatty"
)

func terminal(v any) bool {
	f, ok := v.(interface{ Fd() uintptr })
	return ok && isatty.IsTerminal(f.Fd())
}
func (a *app) machine() bool { return a.forceJSON || !a.outputTTY }
func (a *app) paint(code, text string) string {
	if a.machine() || !a.colors {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Terminal control sequences from agents, paths and reasons are data, never
// styling or cursor movement. Only the renderer adds ANSI escape sequences.
func terminalSafe(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func label(key string) string {
	known := map[string]string{"id": "ID", "cpu": "CPU (cores)", "cpu_cores": "CPU (cores)", "memory_bytes": "Memory", "disk_bytes": "Disk", "ttl_ns": "Lease duration", "window_ns": "Observation window", "grace_ns": "Grace period", "cwd": "Working directory", "pid": "PID", "pgid": "Process group", "uid": "UID", "budget_total": "Total budget", "accounted_memory_bytes": "Accounted memory", "observed_at": "Observed", "expires_at": "Expires", "idempotency_key": "Operation key"}
	if text, ok := known[key]; ok {
		return text
	}
	key = strings.ReplaceAll(key, "_", " ")
	r := []rune(key)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return terminalSafe(string(r))
}
func humanBytes(number string) string {
	n, ok := new(big.Int).SetString(number, 10)
	if !ok {
		return number + " bytes"
	}
	abs := new(big.Int).Abs(new(big.Int).Set(n))
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	i := 0
	divisor := big.NewInt(1)
	for i < len(units)-1 && abs.Cmp(new(big.Int).Mul(new(big.Int).Set(divisor), big.NewInt(1024))) >= 0 {
		divisor.Mul(divisor, big.NewInt(1024))
		i++
	}
	if i == 0 {
		return n.String() + " B"
	}
	value := new(big.Rat).SetFrac(n, divisor).FloatString(2)
	value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	return value + " " + units[i]
}
func (a *app) scalar(key string, v any) string {
	if v == nil {
		return a.paint("33", "unknown")
	}
	if n, ok := v.(json.Number); ok {
		if strings.HasSuffix(key, "_bytes") {
			return humanBytes(n.String())
		}
		if strings.HasSuffix(key, "_ns") {
			if value, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
				return time.Duration(value).String()
			}
		}
		return n.String()
	}
	if text, ok := v.(string); ok {
		if strings.HasSuffix(key, "_at") {
			if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
				return at.Format("2006-01-02 15:04:05 MST")
			}
		}
		text = terminalSafe(text)
		if text == "" {
			return "(none)"
		}
		switch text {
		case "active", "available", "released", "responded", "acknowledged", "complete":
			return a.paint("32", text)
		case "failed", "revoked", "rejected":
			return a.paint("31", text)
		case "queued", "ending", "needs_attention", "reclaiming", "unknown", "unavailable", "preserved", "expired":
			return a.paint("33", text)
		}
		return text
	}
	if value, ok := v.(bool); ok {
		if value {
			return "yes"
		}
		return "no"
	}
	return terminalSafe(fmt.Sprint(v))
}
func orderedKeys(value map[string]any) []string {
	priority := []string{"id", "host", "name", "state", "outcome", "reason", "owner", "agent", "project", "request", "resources", "capacity", "reserved", "available", "started_at", "expires_at", "detail"}
	keys := make([]string, 0, len(value))
	used := map[string]bool{}
	for _, key := range priority {
		if _, ok := value[key]; ok {
			keys = append(keys, key)
			used[key] = true
		}
	}
	var rest []string
	for key := range value {
		if !used[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}
func (a *app) tree(w io.Writer, value any, indent int) {
	pad := strings.Repeat("  ", indent)
	switch value := value.(type) {
	case map[string]any:
		for _, key := range orderedKeys(value) {
			if (key == "owner_username" || key == "actor_username") && value[strings.TrimSuffix(key, "_username")] != nil {
				continue
			}
			child := value[key]
			if (key == "owner" || key == "actor") && value[key+"_username"] != nil {
				fmt.Fprintln(w, pad+label(key)+": "+accountLabel(value, key))
				continue
			}
			switch child.(type) {
			case map[string]any, []any:
				fmt.Fprintln(w, pad+a.paint("1", label(key))+":")
				a.tree(w, child, indent+1)
			default:
				fmt.Fprintln(w, pad+label(key)+": "+a.scalar(key, child))
			}
		}
	case []any:
		if len(value) == 0 {
			fmt.Fprintln(w, pad+"(none)")
			return
		}
		for i, item := range value {
			switch item.(type) {
			case map[string]any, []any:
				if i > 0 {
					fmt.Fprintln(w)
				}
				a.tree(w, item, indent)
			default:
				fmt.Fprintln(w, pad+"- "+a.scalar("", item))
			}
		}
	default:
		fmt.Fprintln(w, pad+a.scalar("", value))
	}
}
func quantity(value any, unit string) string {
	if value == nil {
		return "unknown"
	}
	if unit == "byte" || unit == "bytes" {
		return humanBytes(fmt.Sprint(value))
	}
	return terminalSafe(fmt.Sprint(value))
}
func (a *app) resourceTable(w io.Writer, rows []any, cpu ...map[string]any) {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	fmt.Fprintln(tw, "RESOURCE\tTOTAL\tRESERVED\tAVAILABLE\tUNIT")
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := fmt.Sprint(row["kind"])
		if port, ok := row["port"].(map[string]any); ok {
			name = fmt.Sprint(port["network"]) + "://" + net.JoinHostPort(fmt.Sprint(port["address"]), fmt.Sprint(port["port"]))
		}
		if custom, ok := row["custom"].(map[string]any); ok {
			if pool, ok := custom["pool"].(map[string]any); ok {
				name = fmt.Sprint(pool["name"])
			}
		}
		unit := fmt.Sprint(row["unit"])
		total, reserved, available := row["budget_total"], row["reserved"], row["available"]
		if row["kind"] == "cpu" {
			name = "cpu (live)"
			unit = "cores"
			known := row["availability_known"] == true && row["stale"] != true
			if len(cpu) > 0 {
				known = cpuBudgetMapKnown(cpu[0])
				if cpu[0] != nil {
					total = cpu[0]["capacity_cores"]
					reserved = cpu[0]["reserved_cores"]
					available = cpu[0]["available_cores"]
				}
			}
			if !known {
				total = nil
				available = nil
			}
			total, reserved, available = cpuDisplayValue(total), cpuDisplayValue(reserved), cpuDisplayValue(available)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", terminalSafe(name), quantity(total, unit), quantity(reserved, unit), quantity(available, unit), terminalSafe(unit))
	}
	tw.Flush()
	fmt.Fprintln(w, "\nAvailability describes what ARM can promise now; physical measurements have separate scopes.")
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		users, _ := row["users"].([]any)
		if len(users) > 0 {
			fmt.Fprintln(w, "\n"+a.paint("1", label(fmt.Sprint(row["kind"]))+" consumers:"))
		}
		for _, user := range users {
			u, ok := user.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(w, "  %s  %s\n", a.paint("1", terminalSafe(fmt.Sprint(u["lease"]))), a.scalar("state", u["state"]))
			fmt.Fprintf(w, "    Reserved: %s %s\n", quantity(u["quantity"], fmt.Sprint(row["unit"])), terminalSafe(fmt.Sprint(row["unit"])))
			fmt.Fprintf(w, "    %s; owner %s", terminalSafe(fmt.Sprint(u["reason"])), accountLabel(u, "owner"))
			if agent, ok := u["agent"].(string); ok && agent != "" {
				fmt.Fprintf(w, "; agent %s", terminalSafe(agent))
			}
			fmt.Fprintln(w)
		}
	}
}
func (a *app) human(v any) error {
	if report, ok := v.(model.PeerOverview); ok {
		return a.peersHuman(report)
	}
	if report, ok := v.(install.Report); ok {
		return a.installationReport(report)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err = decoder.Decode(&value); err != nil {
		return err
	}
	var b bytes.Buffer
	fmt.Fprintln(&b, a.paint("1;36", "ARM · "+label(a.commandName)))
	if h, ok := commandHelps[a.commandName]; ok && h.summary != "" {
		summary := strings.ReplaceAll(h.summary, "Output is JSON.", "")
		fmt.Fprintln(&b, strings.TrimSpace(summary))
	}
	fmt.Fprintln(&b)
	if a.commandName == "resources" {
		rows, _ := value.([]any)
		a.resourceTable(&b, rows)
	} else if a.commandName == "status" || a.commandName == "daemon status" {
		if state, ok := value.(map[string]any); ok {
			fmt.Fprintln(&b, "Host: "+a.scalar("host", state["host"]))
			fmt.Fprintln(&b)
			rows, _ := state["resources"].([]any)
			cpu, _ := state["cpu_budget"].(map[string]any)
			if cpu == nil {
				cpu, _ = state["cpu"].(map[string]any)
			} // remote allowlisted summary, never localhost
			if cpu == nil {
				for _, raw := range rows {
					if row, ok := raw.(map[string]any); ok && row["kind"] == "cpu" {
						cpu = map[string]any{"reserved_cores": row["reserved"]}
						break
					}
				}
			}
			a.resourceTable(&b, rows, cpu)
			a.cpuBudgetStatus(&b, cpu)
			leases, _ := state["leases"].([]any)
			fmt.Fprintf(&b, "\nLeases (%d)\n", len(leases))
			for _, raw := range leases {
				l, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				fmt.Fprintf(&b, "  %s  %s", a.scalar("id", l["id"]), a.scalar("state", l["state"]))
				if req, ok := l["request"].(map[string]any); ok {
					fmt.Fprint(&b, "  "+a.scalar("reason", req["reason"]))
				}
				fmt.Fprintln(&b)
			}
		} else {
			a.tree(&b, value, 0)
		}
	} else {
		a.tree(&b, value, 0)
	}
	_, err = a.out.Write(b.Bytes())
	return err
}
func (a *app) reportError(err error) {
	var api *model.Error
	if !errors.As(err, &api) {
		code := "operation_failed"
		var argument *argumentError
		if errors.As(err, &argument) {
			code = "invalid_request"
		}
		api = &model.Error{Code: code, Message: err.Error()}
	}
	if a.machine() || !a.errorTTY {
		_ = json.NewEncoder(a.err).Encode(map[string]any{"error": api})
		return
	}
	heading, color, message := "Error: ", "1;31", api.Message
	if api.Code == "confirmation_declined" || api.Code == "interrupted" {
		heading, color = "Cancelled: ", "1;33"
		message = strings.TrimPrefix(message, "Cancelled; ")
	}
	fmt.Fprintln(a.err, a.paint(color, heading)+terminalSafe(message))
	fmt.Fprintln(a.err, a.paint("2", "Code: "+terminalSafe(api.Code)))
}

func (a *app) notice(text string) {
	if a.machine() || !a.errorTTY {
		_ = json.NewEncoder(a.err).Encode(map[string]any{"notice": text})
	} else {
		fmt.Fprintln(a.err, a.paint("33", terminalSafe(text)))
	}
}
