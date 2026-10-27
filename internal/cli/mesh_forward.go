package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/meshsshrelay"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
)

func (a *app) meshForward(args []string) (int, error) {
	if a.group("mesh forward", args) {
		return 0, nil
	}
	if len(args) == 0 {
		return 2, fmt.Errorf("usage: arm mesh forward grant|revoke|show|start|status|list|stop|recover|stream")
	}
	action, args := args[0], args[1:]
	if !meshsshrelay.ForwardReviewEnabled && (action == "grant" || action == "start" || action == "stream") {
		return 1, model.Err("feature_unavailable", "TCP forwarding is disabled while acceptance gates remain incomplete")
	}
	if action == "start" {
		return a.meshForwardStart(args)
	}
	if action == "stream" {
		return a.meshForwardStream(args)
	}
	if action == "status" || action == "list" || action == "stop" || action == "recover" {
		return a.meshForwardSourceAction(action, args)
	}
	var identity mesh.Identity
	if err := a.call("mesh_identity", struct{}{}, &identity); err != nil {
		return 1, err
	}
	if action == "show" {
		if len(args) != 0 {
			return 2, fmt.Errorf("show accepts no arguments")
		}
		p, err := meshsshrelay.ReadPolicy(a.home, identity.Host, identity.Fingerprint)
		if err != nil {
			return 1, err
		}
		return 0, a.print(p.Forwards)
	}
	if action != "grant" && action != "revoke" {
		return 2, fmt.Errorf("unknown TCP forward action")
	}
	f := a.flags("mesh forward " + action)
	workspaceID := f.String("workspace", "", "exact admitted workspace ID")
	source := f.String("source", "", "exact original source host ID")
	service := f.String("service", "", "destination-local service name")
	address := f.String("target-address", "", "destination-local literal loopback IP:port")
	generation := f.String("generation", "", "exact workspace generation for revocation")
	connections := f.Int("connections", 2, "maximum simultaneous original-source streams")
	transitCPU := f.Float64("transit-cpu", .05, "destination CPU reservation per active connection")
	transitMemoryText := f.String("transit-memory", "8MiB", "destination memory reservation per active connection")
	bytesText := f.String("bytes", "1GiB", "maximum bytes per direction and connection")
	rateText := f.String("rate", "8MiB", "shared bytes per second")
	ttl := f.Duration("ttl", 10*time.Minute, "maximum connection lifetime")
	expires := f.Duration("expires", 24*time.Hour, "grant expiry from review")
	reason := f.String("reason", "", "required review reason")
	yes := f.Bool("yes", false, "apply exact reviewed policy")
	dry := f.Bool("dry-run", false, "show exact review only")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || *workspaceID == "" || *source == "" || *service == "" || *reason == "" || *expires <= 0 || *expires > 365*24*time.Hour {
		return 2, fmt.Errorf("exact workspace, original source, service, reason and bounded expiry required")
	}
	bytes, err := sshBytes(*bytesText)
	if err != nil {
		return 2, err
	}
	rate, err := sshBytes(*rateText)
	if err != nil {
		return 2, err
	}
	transitMemory, err := sshBytes(*transitMemoryText)
	if err != nil {
		return 2, err
	}
	var access workspace.RouteAccess
	if action == "grant" {
		access, err = a.forwardContext(a.ctx, *workspaceID, *source, identity.Host)
		if err != nil {
			return 1, err
		}
		if access.Local.Host != identity.Host || access.Destination.Host != identity.Host {
			return 1, model.Err("forbidden", "local destination membership changed")
		}
		if *generation != "" && *generation != access.Generation {
			return 1, model.Err("forbidden", "workspace generation changed")
		}
	}
	account, err := user.Current()
	if err != nil {
		return 1, err
	}
	change := func(p *meshsshrelay.Policy) error {
		if action == "grant" {
			g := meshforward.Grant{Workspace: access.Workspace, Generation: access.Generation, Source: access.Origin.Host, Target: access.Destination.Host,
				SourceGeneration: access.OriginGeneration, TargetGeneration: access.DestinationGeneration, DirectoryPin: access.Directory.Fingerprint, DirectorySPKI: access.Directory.SPKI,
				SourcePin: access.Origin.Fingerprint, SourceSPKI: access.Origin.SPKI, TargetPin: access.Destination.Fingerprint, TargetSPKI: access.Destination.SPKI,
				Account: account.Username, Endpoint: meshforward.Endpoint{Name: *service, Network: "tcp", Address: *address}, Revision: 1,
				MaxConnections: *connections, BytesPerConnection: bytes, BytesPerSecond: rate, Transit: model.Resources{CPU: *transitCPU, Memory: transitMemory}, MaxTTL: *ttl, Expires: time.Now().UTC().Add(*expires)}
			if err := g.Validate(); err != nil {
				return err
			}
			for i, old := range p.Forwards {
				if old.Workspace == g.Workspace && old.Generation == g.Generation && old.Source == g.Source && old.Endpoint.Name == g.Endpoint.Name {
					g.Revision = old.Revision + 1
					p.Forwards[i] = g
					return nil
				}
			}
			p.Forwards = append(p.Forwards, g)
			return nil
		}
		if *generation == "" {
			return errors.New("exact --generation required for revocation")
		}
		for i, old := range p.Forwards {
			if old.Workspace == *workspaceID && old.Generation == *generation && old.Source == *source && old.Endpoint.Name == *service && old.Target == identity.Host {
				p.Forwards = append(p.Forwards[:i], p.Forwards[i+1:]...)
				return nil
			}
		}
		return errors.New("exact TCP service grant absent")
	}
	reviewAction := "grant"
	if action == "revoke" {
		reviewAction = "revoke"
	}
	review, err := meshsshrelay.ReviewPolicy(a.home, identity.Host, identity.Fingerprint, reviewAction, *reason, change)
	if err != nil {
		return 1, err
	}
	plan := map[string]any{"operation": "forward " + action, "before_sha256": review.BeforeSHA256, "after_sha256": review.AfterSHA256, "before": review.Before.Forwards, "after": review.After.Forwards, "effect": "Exact original source and destination-local literal TCP service only; no SSH or sync authority."}
	if err = a.meshConfirm(plan, *yes, *dry); err != nil {
		return 1, err
	}
	if *dry {
		return 0, nil
	}
	if err = meshsshrelay.CommitPolicy(review); err != nil {
		return 1, err
	}
	return 0, a.print(review.After.Forwards)
}

func (a *app) meshForwardStart(args []string) (int, error) {
	f := a.flags("mesh forward start")
	workspaceID := f.String("workspace", "", "exact workspace ID")
	target := f.String("target", "", "exact destination stable ID")
	service := f.String("service", "", "exact reviewed destination service")
	revision := f.Int64("revision", 0, "reviewed destination service grant revision")
	bind := f.String("bind", "127.0.0.1:0", "local literal loopback IP:port")
	tokenFile := f.String("token-file", "", "new private local access-token file")
	key := f.String("key", "", "stable listener key for exact status after an uncertain reply")
	reason := f.String("reason", "TCP forward source listener", "local resource reservation reason")
	ttl := f.Duration("ttl", 10*time.Minute, "source listener and maximum connection lifetime")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || *workspaceID == "" || *target == "" || *service == "" || *revision < 1 || *tokenFile == "" || *reason == "" || *ttl <= 0 || *ttl > meshsshrelay.MaxLifetime {
		return 2, errors.New("exact workspace, target, service, revision, token file, reason and bounded TTL required")
	}
	host, _, err := net.SplitHostPort(*bind)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return 2, errors.New("source listener requires literal loopback IP")
	}
	if *key == "" {
		*key = local.ID()
	}
	if len(*key) != 32 {
		return 2, errors.New("source listener key must be 16-byte lowercase hex")
	}
	if _, err := hex.DecodeString(*key); err != nil || strings.ToLower(*key) != *key {
		return 2, errors.New("source listener key must be 16-byte lowercase hex")
	}
	var tokenBytes [32]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return 1, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	digest, err := meshforward.TokenDigest(token)
	if err != nil {
		return 1, err
	}
	file, err := os.OpenFile(*tokenFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 1, err
	}
	if _, err = io.WriteString(file, token+"\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(*tokenFile)
		return 1, err
	}
	q := struct {
		Key           string        `json:"key"`
		Workspace     string        `json:"workspace"`
		Target        string        `json:"target"`
		Service       string        `json:"service"`
		GrantRevision int64         `json:"grant_revision"`
		Bind          string        `json:"bind"`
		TokenSHA256   string        `json:"token_sha256"`
		TTL           time.Duration `json:"ttl_ns"`
		Reason        string        `json:"reason"`
	}{*key, *workspaceID, *target, *service, *revision, *bind, digest, *ttl, *reason}
	var status map[string]any
	if err = a.call("mesh_forward_start", q, &status); err != nil {
		return 1, fmt.Errorf("source listener key %s may have an unknown outcome; token retained at %s; inspect mesh forward status --key %s: %w", *key, *tokenFile, *key, err)
	}
	status["token_file"] = *tokenFile
	status["authentication"] = "ARM-FORWARD/1 token line or HTTP Proxy-Authorization: Bearer token"
	return 0, a.print(status)
}

func (a *app) meshForwardSourceAction(action string, args []string) (int, error) {
	f := a.flags("mesh forward " + action)
	key := f.String("key", "", "exact source listener key")
	reason := f.String("reason", "", "required recovered-listener cleanup reason")
	yes := f.Bool("yes", false, "confirm exact recovered-listener cleanup")
	dry := f.Bool("dry-run", false, "review recovered-listener cleanup")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if f.NArg() != 0 || action != "list" && *key == "" {
		return 2, errors.New("exact --key required for source listener status or stop")
	}
	if action == "list" {
		if *key != "" || *reason != "" || *yes || *dry {
			return 2, errors.New("list accepts no key")
		}
		var rows []map[string]any
		if err := a.call("mesh_forward_list", struct{}{}, &rows); err != nil {
			return 1, err
		}
		return 0, a.print(rows)
	}
	if action == "recover" {
		if *reason == "" {
			return 2, errors.New("exact --key and --reason required for recovered-listener cleanup")
		}
		var before map[string]any
		if err := a.call("mesh_forward_status", struct {
			Key string `json:"key"`
		}{*key}, &before); err != nil {
			return 1, err
		}
		plan := map[string]any{"operation": "recover original TCP source listener port and resource charge", "before": before, "reason": *reason, "effect": "Exact port probe and terminal charge only; application bytes remain unknown and are never replayed"}
		if err := a.meshConfirm(plan, *yes, *dry); err != nil {
			return 1, err
		}
		if *dry {
			return 0, nil
		}
		var after map[string]any
		if err := a.call("mesh_forward_recover", struct {
			Key    string `json:"key"`
			Reason string `json:"reason"`
		}{*key, *reason}, &after); err != nil {
			return 1, err
		}
		return 0, a.print(after)
	}
	if *reason != "" || *yes || *dry {
		return 2, errors.New("reason and review flags apply only to recover")
	}
	var status map[string]any
	if err := a.call("mesh_forward_"+action, struct {
		Key string `json:"key"`
	}{*key}, &status); err != nil {
		return 1, err
	}
	return 0, a.print(status)
}
