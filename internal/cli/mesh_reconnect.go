package cli

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
)

func (a *app) meshReconnect(args []string) (int, error) {
	f := a.flags("mesh reconnect")
	destination := f.String("ssh", "", "replacement verified SSH locator; otherwise use the exact retained enrollment record")
	connector := f.String("connector", "", "immutable native connector ID (instead of explicit locator/helper options)")
	binary := f.String("binary", "", "explicit safe installed ARM helper; otherwise retain its recorded path")
	home := f.String("remote-home", "", "explicit remote ARM home when no verified home is retained")
	grant := f.String("grant", "observe-host", "new permissions on target; previous grants are never restored")
	reverse := f.String("reverse-grant", "", "new target permissions here; none by default")
	contacts := f.String("contacts", "", "exact contact IDs for newly selected contact permissions")
	inbox := f.Bool("operator-inbox", true, "operator inbox only when message is explicitly granted")
	cpu := f.Float64("cpu", 2, "new per-peer aggregate CPU ceiling")
	memory := f.String("memory", "1GiB", "new per-peer memory ceiling")
	disk := f.String("disk", "1GiB", "new per-peer disk ceiling")
	ttl := f.Duration("max-ttl", time.Hour, "new maximum granted remote lease lifetime")
	reason := f.String("reason", "operator reconnected exact ARM node", "audited purpose for this new enrollment")
	key := f.String("key", "", "exact immutable reconnect key (generated for a new intent)")
	localIP := f.String("local-ip", "", "explicit local assigned interface IP")
	remoteIP := f.String("remote-ip", "", "explicit target assigned interface IP")
	public := f.Bool("allow-public-listen", false, "permit separately reviewed nonprivate exposure")
	resume := f.Bool("resume", false, "prepare explicit recovery of an inactive incomplete --key; ordinary retry only reads")
	yes := f.Bool("yes", false, "authorize the concrete new enrollment review")
	dry := f.Bool("dry-run", false, "read-only plan; no backend, identity, key or socket creation")
	if e := a.parse(f, args, false); e != nil {
		return 2, e
	}
	if f.NArg() != 1 {
		return 2, fmt.Errorf("reconnect requires one exact stable HOST_ID")
	}
	if *resume && *key == "" {
		return 2, fmt.Errorf("--resume requires the original explicit --key")
	}
	m, e := bytesValue(*memory)
	if e != nil {
		return 2, e
	}
	d, e := bytesValue(*disk)
	if e != nil {
		return 2, e
	}
	makeGrant := func(text string) mesh.Grant {
		p := []string{}
		if text != "" {
			p = strings.Split(text, ",")
		}
		ids := []string{}
		if *contacts != "" {
			ids = strings.Split(*contacts, ",")
		}
		return mesh.Grant{Permissions: p, Contacts: ids, OperatorInbox: *inbox, Limits: model.Resources{CPU: *cpu, Memory: m, Disk: d}, MaxTTL: *ttl}
	}
	draft := meshops.ReconnectDraft{Host: f.Arg(0), Destination: *destination, Connector: *connector, Binary: *binary, RemoteHome: *home, LocalIP: *localIP, RemoteIP: *remoteIP, Public: *public, Reason: *reason, Forward: makeGrant(*grant), Reverse: makeGrant(*reverse)}
	if *key == "" {
		*key = local.ID() + local.ID()
	}

	// A known immutable key reads its original draft offline. Only options the
	// caller explicitly supplied can conflict; defaults cannot silently recapture
	// current trust or overwrite a previously reviewed selection.
	if op, ok, err := mesh.ReadCoordination(a.home, *key); err != nil {
		return 1, err
	} else if ok {
		var saved meshops.StartRequest
		if err = mesh.Decode(op.Request, &saved); err != nil {
			return 1, err
		}
		if op.Kind != "reconnect" || saved.Kind != "reconnect" || saved.Reconnect == nil || saved.Key != *key {
			return 1, model.Err("idempotency_conflict", "key belongs to another immutable operation")
		}
		selected := saved.Reconnect.Draft
		selected.Host = draft.Host
		f.Visit(func(v *flag.Flag) {
			switch v.Name {
			case "ssh":
				selected.Destination = draft.Destination
			case "connector":
				selected.Connector = draft.Connector
			case "binary":
				selected.Binary = draft.Binary
			case "remote-home":
				selected.RemoteHome = draft.RemoteHome
			case "local-ip":
				selected.LocalIP = draft.LocalIP
			case "remote-ip":
				selected.RemoteIP = draft.RemoteIP
			case "allow-public-listen":
				selected.Public = draft.Public
			case "reason":
				selected.Reason = draft.Reason
			case "grant":
				selected.Forward.Permissions = draft.Forward.Permissions
			case "reverse-grant":
				selected.Reverse.Permissions = draft.Reverse.Permissions
			case "contacts":
				selected.Forward.Contacts = draft.Forward.Contacts
				selected.Reverse.Contacts = draft.Reverse.Contacts
			case "operator-inbox":
				selected.Forward.OperatorInbox = draft.Forward.OperatorInbox
				selected.Reverse.OperatorInbox = draft.Reverse.OperatorInbox
			case "cpu":
				selected.Forward.Limits.CPU = draft.Forward.Limits.CPU
				selected.Reverse.Limits.CPU = draft.Reverse.Limits.CPU
			case "memory":
				selected.Forward.Limits.Memory = draft.Forward.Limits.Memory
				selected.Reverse.Limits.Memory = draft.Reverse.Limits.Memory
			case "disk":
				selected.Forward.Limits.Disk = draft.Forward.Limits.Disk
				selected.Reverse.Limits.Disk = draft.Reverse.Limits.Disk
			case "max-ttl":
				selected.Forward.MaxTTL = draft.Forward.MaxTTL
				selected.Reverse.MaxTTL = draft.Reverse.MaxTTL
			}
		})
		draft = selected
	}
	request, e := meshops.ResolveReconnect(a.home, *key, draft)
	if e != nil {
		return 1, e
	}
	c := a.meshCoordinator()
	if *resume {
		return c.ResumeReconnect(*key, request, *yes, *dry)
	}
	return c.Reconnect(*key, request, *yes, *dry)
}
