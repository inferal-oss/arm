package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
	"io"
	"regexp"
	"strings"
	"time"
)

func (a *app) hostCall(host, operation string, params, result any) error {
	return a.hostCallKey(host, operation, "", params, result)
}
func (a *app) hostCallKey(host, operation, explicitKey string, params, result any) error {
	if host == "" || host == "localhost" {
		return a.call(operation, params, result)
	}
	var localStatus model.Status
	if e := a.call("status", nil, &localStatus); e != nil {
		return e
	}
	if host == localStatus.Host {
		return a.call(operation, params, result)
	}
	raw, e := json.Marshal(params)
	if e != nil {
		return e
	}
	key := explicitKey
	if mesh.Mutating(operation) && key == "" {
		key = local.ID()
		if operation == "acquire" {
			if q, ok := params.(model.Request); ok {
				key = q.Key
			}
		}
		if operation == "run" {
			if q, ok := params.(manager.RunRequest); ok {
				key = q.Request.Key
			}
		}
	}
	err := a.call("mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{host, mesh.Request{Operation: operation, Key: key, Params: raw}}, result)
	if err != nil && mesh.Mutating(operation) {
		if api, ok := err.(*model.Error); ok {
			return &model.Error{Code: api.Code, Message: fmt.Sprintf("%s (target %s, operation %s, exact-retry key %q)", api.Message, host, operation, key)}
		}
		return model.Err("remote_outcome_unknown", fmt.Sprintf("target %s operation %s key %q: %v", host, operation, key, err))
	}
	return err
}
func (a *app) meshConfirm(plan any, yes, dry bool) error {
	if dry || !a.machine() || !yes {
		if e := a.print(plan); e != nil {
			return e
		}
	}
	if dry {
		return nil
	}
	if yes {
		return nil
	}
	if !a.inputTTY || a.machine() {
		return model.Err("confirmation_required", "review --dry-run then pass --yes for mesh trust/listener changes")
	}
	fmt.Fprint(a.out, "\n"+a.paint("1;33", "Apply this mesh change? [y/N] "))
	type answer struct {
		line string
		err  error
	}
	reply := make(chan answer, 1)
	go func() {
		line, e := bufio.NewReader(io.LimitReader(a.in, 257)).ReadString('\n')
		reply <- answer{line, e}
	}()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case input := <-reply:
		if input.err != nil || len(input.line) > 256 || !(strings.EqualFold(strings.TrimSpace(input.line), "yes") || strings.EqualFold(strings.TrimSpace(input.line), "y")) {
			return model.Err("confirmation_declined", "mesh change declined")
		}
		return nil
	}
}

type meshBootstrap = meshops.Bootstrap

func (a *app) meshCommand(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("mesh requires connect, disconnect, lookup, identity, peers or an advanced operation")
	}
	action, args := args[0], args[1:]
	if action == "connector" {
		return a.meshConnector(args)
	}
	if action == "connect" || action == "disconnect" {
		return a.meshLifecycle(action, args)
	}
	f := a.flags("mesh " + action)
	yes, dry := new(bool), new(bool)
	if action == "init" || action == "join" || action == "revoke" || action == "renew" {
		f.BoolVar(yes, "yes", false, "apply the reviewed trust/listener change")
		f.BoolVar(dry, "dry-run", false, "preview without mutation")
	}
	switch action {
	case "provision-finalize", "provision-lookup":
		return a.meshProvisionHelper(action, args)
	case "artifacts":
		return a.meshArtifacts(args)
	case "operations":
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() > 1 {
			return 2, fmt.Errorf("mesh operations accepts at most one exact key")
		}
		if f.NArg() == 1 {
			op, ok, e := mesh.ReadCoordination(a.home, f.Arg(0))
			if e != nil {
				return 1, e
			}
			if !ok {
				return 1, model.Err("not_found", "operator workflow key not found")
			}
			return 0, a.print(meshops.Project(op))
		}
		ops, e := mesh.ListCoordinations(a.home)
		if e != nil {
			return 1, e
		}
		out := make([]meshops.Operation, 0, len(ops))
		for _, op := range ops {
			out = append(out, meshops.ProjectSummary(op))
		}
		return 0, a.print(out)
	case "lookup":
		if err := a.parse(f, args, false); err != nil {
			return 2, err
		}
		if f.NArg() != 1 {
			return 2, fmt.Errorf("mesh lookup requires lifecycle KEY")
		}
		operation, exists, err := mesh.ReadLifecycleOperation(a.home, f.Arg(0))
		if err != nil {
			return 1, err
		}
		if !exists {
			return 1, model.Err("not_found", "no durable local mesh lifecycle record for this key")
		}
		return 0, a.print(operation)
	case "identity", "peers":
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		var result any
		if e := a.call("mesh_"+action, nil, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "init":
		name := f.String("name", "", "unique friendly node name")
		listen := f.String("listen", "", "explicit literal-IP:PORT mesh listener; no wildcard default")
		address := f.String("address", "", "reachable HOST:PORT advertised to direct peers")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if *name == "" || *listen == "" || *address == "" {
			return 2, fmt.Errorf("mesh init requires --name, --listen and --address")
		}
		plan := map[string]any{"operation": "initialize-mesh", "name": *name, "listen": *listen, "address": *address, "changes": "create private node key/certificate and peer configuration under ARM state; enable separate TLS1.3 listener; no peer is trusted"}
		if e := a.meshConfirm(plan, *yes, *dry); e != nil {
			return 1, e
		}
		if *dry {
			return 0, nil
		}
		var result any
		if e := a.call("mesh_init", map[string]string{"Name": *name, "Listen": *listen, "Address": *address}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "bootstrap": // SSH-only account-authorized helper; no mesh RPC can call it.
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		var q meshBootstrap
		raw, e := io.ReadAll(io.LimitReader(a.in, (1<<20)+1))
		if e != nil || len(raw) > 1<<20 {
			return 2, fmt.Errorf("bootstrap payload exceeds 1MiB")
		}
		var header struct{ Action string }
		if e = json.Unmarshal(raw, &header); e != nil {
			return 2, e
		}
		if header.Action == "provision_runtime" {
			return a.meshProvisionRuntimeHelper(raw)
		}
		if header.Action == "provision_start" {
			return a.meshProvisionStartHelper(raw)
		}
		if strings.HasPrefix(header.Action, "lifecycle_") {
			return a.meshLifecycleBootstrap(raw)
		}
		if e = mesh.Decode(raw, &q); e != nil {
			return 2, e
		}
		if q.Home != "" {
			a.home = q.Home
		}
		var result any
		switch q.Action {
		case "identity":
			if e := a.call("mesh_identity", nil, &result); e != nil {
				return 1, e
			}
		case "peers":
			if e := a.call("mesh_peers", nil, &result); e != nil {
				return 1, e
			}
		case "pair":
			if e := a.call("mesh_pair", struct {
				Identity         mesh.Identity
				Grant            mesh.Grant
				Pair, Reason     string
				ReplaceRevoked   bool
				ExpectedRevision *int
			}{q.Identity, q.Grant, q.Pair, q.Reason, q.ReplaceRevoked, q.ExpectedRevision}, &result); e != nil {
				return 1, e
			}
		case "activate":
			if e := a.call("mesh_activate", map[string]string{"Host": q.Host, "Pair": q.Pair}, &result); e != nil {
				return 1, e
			}
		default:
			return 2, fmt.Errorf("unknown SSH bootstrap action")
		}
		return 0, a.print(result)
	case "renew":
		reason := f.String("reason", "", "required certificate renewal/recovery reason")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if *reason == "" {
			return 2, fmt.Errorf("mesh renew requires --reason")
		}
		var config mesh.Config
		if e := a.call("mesh_recovery_identity", nil, &config); e != nil {
			return 1, e
		}
		plan := map[string]any{"operation": "renew-node-certificate", "host": config.Identity.Host, "current_fingerprint": config.Identity.Fingerprint, "stable_spki": config.Identity.SPKI, "current_expiry": config.Identity.Expires, "effect": "issue new one-year self-signed certificate using existing key; close mesh sessions, revoke peer grants locally and require explicit reviewed SSH re-pair on both nodes; private key rotation is not performed", "affected_peer_records": len(config.Peers), "reason": *reason}
		if e := a.meshConfirm(plan, *yes, *dry); e != nil {
			return 1, e
		}
		if *dry {
			return 0, nil
		}
		var result any
		if e := a.call("mesh_renew", map[string]string{"Reason": *reason}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "join":
		replaceRevoked := f.Bool("replace-revoked", false, "explicitly replace a revoked peer record with new reviewed identity/grants and a fresh proof")
		destination := f.String("ssh", "", "operator SSH destination with an already verified known_hosts key")
		remoteHome := f.String("remote-home", "", "target ARM state directory (default target user config)")
		binary := f.String("binary", "arm", "remote installed ARM executable; simple absolute path or arm")
		grants := f.String("grant", "observe-host", "comma-separated directional permissions this node receives on target")
		reverse := f.String("reverse-grant", "", "optional directional target permissions on this node")
		contacts := f.String("contacts", "", "comma-separated exact target contact IDs allowed to contact-read/message grants")
		operatorInbox := f.Bool("operator-inbox", true, "allow message grant to target operator inbox")
		reason := f.String("reason", "", "required pairing purpose")
		pair := f.String("key", "", "pairing operation key for exact resume (generated)")
		cpu := f.Float64("cpu", 2, "aggregate CPU ceiling for granted peer")
		memory := f.String("memory", "1GiB", "aggregate memory ceiling")
		disk := f.String("disk", "1GiB", "aggregate disk ceiling")
		ttl := f.Duration("max-ttl", time.Hour, "maximum remote lease/renew lifetime")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if *destination == "" || strings.HasPrefix(*destination, "-") || *reason == "" || !regexp.MustCompile(`^(arm|/[A-Za-z0-9_./-]+)$`).MatchString(*binary) {
			return 2, fmt.Errorf("join requires safe --ssh destination, fixed remote ARM binary and --reason")
		}
		mem, e := bytesValue(*memory)
		if e != nil {
			return 2, e
		}
		d, e := bytesValue(*disk)
		if e != nil {
			return 2, e
		}
		makeGrant := func(text string) mesh.Grant {
			permissions := []string{}
			if text != "" {
				permissions = strings.Split(text, ",")
			}
			ids := []string{}
			if *contacts != "" {
				ids = strings.Split(*contacts, ",")
			}
			return mesh.Grant{Contacts: ids, OperatorInbox: *operatorInbox, Permissions: permissions, Limits: model.Resources{CPU: *cpu, Memory: mem, Disk: d}, MaxTTL: *ttl}
		}
		forward, back := makeGrant(*grants), makeGrant(*reverse)
		if e = forward.Validate(); e != nil {
			return 2, e
		}
		if e = back.Validate(); e != nil {
			return 2, e
		}
		if *pair == "" {
			*pair = local.ID() + local.ID()
		}
		var own, remote mesh.Identity
		if e = a.call("mesh_identity", nil, &own); e != nil {
			return 1, e
		}
		var verifiedSSH meshSSHIdentity
		ssh := func(q meshBootstrap, result any) error {
			q.Home = *remoteHome
			observed, err := a.meshSSH(*destination, *binary, q, verifiedSSH, result)
			if err == nil {
				verifiedSSH = observed
			}
			return err
		}
		if e = ssh(meshBootstrap{Action: "identity"}, &remote); e != nil {
			return 1, e
		}
		if _, e = mesh.ValidateIdentity(remote); e != nil {
			return 1, e
		}
		var priorRemote, priorLocal []mesh.Peer
		if e = ssh(meshBootstrap{Action: "peers"}, &priorRemote); e != nil {
			return 1, e
		}
		if e = a.call("mesh_peers", nil, &priorLocal); e != nil {
			return 1, e
		}
		prior := func(peers []mesh.Peer, host string) any {
			for _, p := range peers {
				if p.Identity.Host == host {
					return map[string]any{"host": p.Identity.Host, "name": p.Identity.Name, "fingerprint": p.Identity.Fingerprint, "spki": p.Identity.SPKI, "state": p.State, "grant": p.Grant, "revision": p.Revision}
				}
			}
			return nil
		}
		plan := map[string]any{"operation": "pair-nodes", "pairing_key": *pair, "source_host": own.Host, "source_name": own.Name, "source_fingerprint": own.Fingerprint, "target_host": remote.Host, "target_name": remote.Name, "target_fingerprint": remote.Fingerprint, "target_address": remote.Address, "ssh_identity": verifiedSSH, "target_os_user": remote.OwnerUsername, "target_os_uid": remote.Owner, "source_spki": own.SPKI, "target_spki": remote.SPKI, "source_certificate_expires": own.Expires, "target_certificate_expires": remote.Expires, "source_permissions_on_target": forward, "target_permissions_on_source": back, "reason": *reason, "replace_revoked": *replaceRevoked, "previous_target_trust": prior(priorRemote, own.Host), "previous_source_trust": prior(priorLocal, remote.Host)}
		if forward.Has("run") || back.Has("run") {
			plan["execution_authority"] = "RUN executes as the target OS user and can read/change that user's files, credentials and ARM keys; it is not a sandbox"
		}
		if e = a.meshConfirm(plan, *yes, *dry); e != nil {
			return 1, e
		}
		if *dry {
			return 0, nil
		}
		var pending any
		pairFailure := func(stage string, err error) error {
			code := "mesh_pair_incomplete"
			if api, ok := err.(*model.Error); ok {
				code = api.Code
			}
			return model.Err(code, fmt.Sprintf("pairing %s with host %s failed during %s; inspect both nodes' pending/active records before an exact --key retry: %v", *pair, remote.Host, stage, err))
		}
		if e = ssh(meshBootstrap{Action: "pair", Identity: own, Grant: forward, Pair: *pair, Reason: *reason, ReplaceRevoked: *replaceRevoked}, &pending); e != nil {
			return 1, pairFailure("reviewed pairing/proof/activation", e)
		}
		if e = a.call("mesh_pair", struct {
			Identity       mesh.Identity
			Grant          mesh.Grant
			Pair, Reason   string
			ReplaceRevoked bool
		}{remote, back, *pair, *reason, *replaceRevoked}, &pending); e != nil {
			return 1, pairFailure("reviewed pairing/proof/activation", e)
		}
		if e = a.call("mesh_prove", map[string]string{"Host": remote.Host, "Pair": *pair}, &pending); e != nil {
			return 1, pairFailure("reviewed pairing/proof/activation", e)
		}
		if e = a.call("mesh_activate", map[string]string{"Host": remote.Host, "Pair": *pair}, nil); e != nil {
			return 1, pairFailure("reviewed pairing/proof/activation", e)
		}
		if e = ssh(meshBootstrap{Action: "activate", Host: own.Host, Pair: *pair}, &pending); e != nil {
			return 1, pairFailure("target activation", e)
		}
		return 0, a.print(map[string]any{"paired": remote.Host, "name": remote.Name, "pairing_key": *pair, "fingerprint": remote.Fingerprint})
	case "revoke":
		reason := f.String("reason", "", "required revocation reason")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 1 || *reason == "" {
			return 2, fmt.Errorf("revoke requires HOST and --reason")
		}
		if e := a.meshConfirm(map[string]any{"operation": "revoke-peer", "host": f.Arg(0), "reason": *reason, "effect": "deny new operations and close peer sessions; existing local leases retain expiry/cleanup obligations"}, *yes, *dry); e != nil {
			return 1, e
		}
		if *dry {
			return 0, nil
		}
		if e := a.call("mesh_revoke", map[string]string{"Host": f.Arg(0), "Reason": *reason}, nil); e != nil {
			return 1, e
		}
		return 0, a.print(map[string]bool{"revoked": true})
	case "call":
		params := f.String("params", "{}", "JSON parameters for an allowed peer operation")
		key := f.String("key", "", "required exact-retry key for mutations")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 2 || !json.Valid([]byte(*params)) {
			return 2, fmt.Errorf("call requires HOST OPERATION and valid --params JSON")
		}
		var result any
		if e := a.call("mesh_dispatch", struct {
			Host    string
			Request mesh.Request
		}{f.Arg(0), mesh.Request{Operation: f.Arg(1), Key: *key, Params: json.RawMessage(*params)}}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "message-lookup":
		key := f.String("key", "", "original message operation key")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 1 || !mesh.ValidKey(*key) {
			return 2, fmt.Errorf("message-lookup requires HOST --key ORIGINAL_KEY")
		}
		var result any
		if e := a.hostCall(f.Arg(0), "message_lookup", map[string]string{"idempotency_key": *key}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "contacts":
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 1 {
			return 2, fmt.Errorf("contacts requires HOST (explicit contact-read grant)")
		}
		var result []agents.Contact
		if e := a.hostCall(f.Arg(0), "contacts", map[string]any{}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	case "send":
		body := f.String("body", "", "message body")
		key := f.String("key", "", "required exact-retry message key")
		to := f.String("to", "operator", "operator or exact contact ID")
		registration := f.String("registration", "", "reviewed remote contact registration (required for agent recipient)")
		if e := a.parse(f, args, false); e != nil {
			return 2, e
		}
		if f.NArg() != 1 || *body == "" || !mesh.ValidKey(*key) {
			return 2, fmt.Errorf("send requires HOST --body TEXT --key KEY [--to CONTACT --registration REVIEWED]")
		}
		q := manager.MessageRequest{Body: *body, Audience: *to, Targets: []agents.Contact{}}
		if *to != "operator" {
			var contacts []agents.Contact
			if e := a.hostCall(f.Arg(0), "contacts", map[string]any{}, &contacts); e != nil {
				return 1, e
			}
			for _, contact := range contacts {
				if contact.ID == *to && contact.Registration == *registration {
					q.Targets = append(q.Targets, contact)
				}
			}
			if len(q.Targets) != 1 {
				return 1, model.Err("contact_changed", "review exact host/contact registration with mesh contacts before sending")
			}
		}
		raw, _ := json.Marshal(q)
		var result any
		if e := a.call("mesh_dispatch", struct {
			Host    string
			Request mesh.Request
		}{f.Arg(0), mesh.Request{Operation: "message", Key: *key, Params: raw}}, &result); e != nil {
			return 1, e
		}
		return 0, a.print(result)
	}
	return 2, fmt.Errorf("unknown mesh command %q", action)
}
