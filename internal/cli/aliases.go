package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"net"
	"os"
	"strings"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func opaqueID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && strings.ToLower(id) == id
}

func aliasError(err error) error {
	if errors.Is(err, readablename.ErrAmbiguous) {
		return model.Err("ambiguous_name", err.Error())
	}
	if errors.Is(err, readablename.ErrIncomplete) {
		return model.Err("name_inventory_unavailable", err.Error())
	}
	return model.Err("name_not_found", err.Error())
}

func hostAliasError(err error) error {
	if errors.Is(err, readablename.ErrNotFound) || errors.Is(err, readablename.ErrAmbiguous) {
		// Existing peer-name reads use this code for both absent and duplicate
		// names. Readable aliases do not change that public error contract.
		return model.Err("host_unavailable", "unknown or ambiguous host name; use its full stable host ID")
	}
	return aliasError(err)
}

// hostAliasInventory is an independent legacy/generated recovery inventory.
// It never reads optional names and never establishes custom alias authority.
func (a *app) hostAliasInventory() ([]readablename.Candidate, string, error) {
	objects, localHost, found, err := a.offlineHostAliases()
	if err != nil || found {
		return objects, localHost, err
	}
	return a.onlineHostAliases()
}
func (a *app) offlineHostAliases() ([]readablename.Candidate, string, bool, error) {
	config, found, err := mesh.ReadRegistry(a.home)
	if err != nil || !found {
		return nil, "", found, err
	}
	objects := []readablename.Candidate{{ID: config.Identity.Host, Name: config.Identity.Name}}
	for _, peer := range config.Peers {
		objects = append(objects, readablename.Candidate{ID: peer.Identity.Host, Name: peer.Identity.Name})
	}
	return objects, config.Identity.Host, true, nil
}
func (a *app) onlineHostAliases() ([]readablename.Candidate, string, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	var registry model.NodeRegistry
	if err := client.Call(ctx, a.home, "node_list", nil, &registry); err != nil {
		return nil, "", err
	}
	if registry.Error != nil {
		return nil, "", aliasError(readablename.ErrIncomplete)
	}
	objects := make([]readablename.Candidate, 0, len(registry.Nodes))
	localHost := ""
	for _, node := range registry.Nodes {
		objects = append(objects, readablename.Candidate{ID: node.Host, Name: node.Name})
		if node.Local {
			if localHost != "" {
				return nil, "", aliasError(readablename.ErrIncomplete)
			}
			localHost = node.Host
		}
	}
	if localHost == "" {
		return nil, "", aliasError(readablename.ErrIncomplete)
	}
	return objects, localHost, nil
}
func (a *app) resolveHost(target string) (string, error) {
	if target == "" || target == "localhost" || opaqueID(target) {
		return target, nil
	}
	var objects []readablename.Candidate
	var err error
	if !selectorNeedsCustom(target, nil) {
		objects, _, err = a.hostAliasInventory()
		if err != nil {
			return "", err
		}
	}
	id, err := a.resolveWithCustom(readablename.Host, target, "", objects, nil)
	if err != nil {
		return "", hostAliasErrorPreserving(err)
	}
	return id, nil
}

// Only explicit offline receipt/recovery commands may use legacy registry
// spellings without a healthy online custom-name map. Authentication, metadata
// and incomplete-protocol failures never authorize that fallback.
func (a *app) resolveHostRecovery(target string) (string, error) {
	id, err := a.resolveHost(target)
	if err == nil || !offlineNamingBackend(err) || a.ctx.Err() != nil {
		return id, err
	}
	objects, _, found, readErr := a.offlineHostAliases()
	if readErr != nil {
		return "", readErr
	}
	if !found {
		return "", err
	}
	id, readErr = readablename.Resolve(readablename.Host, target, objects, true)
	if readErr != nil {
		return "", hostAliasError(readErr)
	}
	return id, nil
}
func offlineNamingBackend(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var detail *model.Error
	if errors.As(err, &detail) {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var network *net.OpError
	return errors.As(err, &network) && network.Op == "dial"
}

// Classification validates local argv versus explicit remote CWD before
// action reads. Its canonical ID is rechecked jointly using the original
// selector before any request is built or dispatched.
func (a *app) executionHost(target string) (string, error) {
	classification, _, err := a.executionHostIdentity(target)
	return classification, err
}
func (a *app) executionHostIdentity(target string) (string, string, error) {
	if target == "" || target == "localhost" || opaqueID(target) {
		return target, target, nil
	}
	objects, localHost, err := a.hostAliasInventory()
	if err != nil {
		return "", "", err
	}
	id, err := readablename.Resolve(readablename.Host, target, objects, true)
	if errors.Is(err, readablename.ErrNotFound) {
		id, err = a.resolveHost(target)
	}
	if err != nil {
		return "", "", hostAliasErrorPreserving(err)
	}
	if id != localHost {
		return id, id, nil
	}
	var status model.Status
	if err = a.call("status", nil, &status); err != nil {
		return "", "", err
	}
	if status.Host == id {
		return "localhost", id, nil
	}
	return id, id, nil
}

// resolveValidatedAliases is called by owning handlers after their argument
// validation, before constructing a request or review. It never runs in the
// parser, help path or completion catalog.
// Every selector is replaced by an original ID before constructing requests,
// idempotency records, review hashes or native execution plans. Exact IDs need
// no inventory lookup, preserving offline/recovery and older-server behavior.
func (a *app) resolveValidatedAliases(f *flag.FlagSet) error {
	if f.Name() == "arm" {
		return nil
	}
	host := "localhost"
	if v := f.Lookup("host"); v != nil {
		id, err := a.resolveHost(v.Value.String())
		if err != nil {
			return err
		}
		if id != v.Value.String() {
			if err = f.Set("host", id); err != nil {
				return err
			}
		}
		host = id
	}
	var leases []readablename.Candidate
	var pools []readablename.Candidate
	authority := ""
	leaseRead, poolRead := false, false
	resolve := func(domain, target string) (string, error) {
		if target == "" || (domain == readablename.Lease && opaqueID(target)) || (domain == readablename.Pool && strings.HasPrefix(target, "pool_") && opaqueID(strings.TrimPrefix(target, "pool_"))) {
			return target, nil
		}
		if domain == readablename.Lease && !leaseRead {
			var status model.Status
			if err := a.hostCall(host, "status", nil, &status); err != nil {
				return "", err
			}
			authority = status.Host
			for _, l := range status.Leases {
				leases = append(leases, readablename.Candidate{ID: l.ID})
			}
			leaseRead = true
		}
		if domain == readablename.Pool && !poolRead {
			if host != "" && host != "localhost" {
				var localStatus model.Status
				if err := a.call("status", nil, &localStatus); err != nil {
					return "", err
				}
				if localStatus.Host != host {
					return "", model.Err("unsupported_remote_claim", "remote pool-name claims are unsupported; select a local authority or use an exact pool ID for its existing backend contract")
				}
			}
			var views []model.PoolView
			// Complete retained definitions, including deleted names, prevent
			// an old alias from silently selecting a replacement obligation.
			if err := a.call("pool_list", map[string]bool{"all": true}, &views); err != nil {
				return "", err
			}
			for _, v := range views {
				pools = append(pools, readablename.Candidate{ID: v.Pool.ID, Name: v.Pool.Name})
			}
			poolRead = true
		}
		objects := leases
		if domain == readablename.Pool {
			objects = pools
		}
		var err error
		if selectorNeedsCustom(target, objects) {
			if authority != "" && host != "" && host != "localhost" && authority != host {
				return "", aliasError(readablename.ErrIncomplete)
			}
			if authority == "" {
				authority, err = a.nameAuthority(host)
				if err != nil {
					return "", err
				}
			}
		}
		id, err := a.resolveWithCustom(domain, target, authority, objects, nil)
		if err != nil {
			return "", aliasErrorPreserving(err)
		}
		return id, nil
	}
	leaseFlags := []string{"lease"}
	if f.Name() == "run" {
		leaseFlags = append(leaseFlags, "storage", "service", "port-lease")
	}
	for _, name := range leaseFlags {
		if v := f.Lookup(name); v != nil && v.Value.String() != "" {
			id, err := resolve(readablename.Lease, v.Value.String())
			if err != nil {
				return err
			}
			if id != v.Value.String() {
				if err = f.Set(name, id); err != nil {
					return err
				}
			}
		}
	}
	if strings.HasPrefix(f.Name(), "pool ") {
		if v := f.Lookup("parent"); v != nil && v.Value.String() != "" {
			id, err := resolve(readablename.Pool, v.Value.String())
			if err != nil {
				return err
			}
			if id != v.Value.String() {
				if err = f.Set("parent", id); err != nil {
					return err
				}
			}
		}
	}
	if v := f.Lookup("pool"); v != nil {
		if claims, ok := v.Value.(*poolFlags); ok {
			for i := range *claims {
				id, err := resolve(readablename.Pool, (*claims)[i].Pool)
				if err != nil {
					return err
				}
				(*claims)[i].Pool = id
			}
		}
	}
	args := f.Args()
	if len(args) == 0 {
		return nil
	}
	switch f.Name() {
	case "ssh", "ssh lookup", "ssh seal", "mesh revoke", "mesh call", "mesh contacts", "mesh send", "mesh message-lookup", "mesh stop", "mesh uninstall", "mesh disconnect", "mesh reconnect":
		resolveHost := a.resolveHost
		if f.Name() == "ssh lookup" || f.Name() == "ssh seal" || f.Name() == "mesh reconnect" {
			resolveHost = a.resolveHostRecovery
		}
		id, err := resolveHost(args[0])
		if err != nil {
			var detail *model.Error
			if strings.HasPrefix(f.Name(), "ssh") && errors.As(err, &detail) && detail.Code == "host_unavailable" {
				// SSH's offline history selector already translates this case
				// to not_found before any execution or routed fallback.
				return model.Err("not_found", "unknown or ambiguous local node name; use the full stable host ID for removed peers")
			}
			return err
		}
		if id != args[0] {
			args = append([]string{}, args...)
			args[0] = id
			if err = f.Parse(append([]string{"--"}, args...)); err != nil {
				return err
			}
		}
	}
	domain := ""
	switch f.Name() {
	case "inspect", "release", "cancel", "renew", "reconcile", "usage", "storage cleanup", "storage handoff", "storage retain", "storage export", "reviews policy", "reviews configure":
		domain = readablename.Lease
	case "pool inspect", "pool history", "pool set", "pool adjust", "pool rename", "pool describe", "pool drain", "pool resume", "pool delete", "pool children", "pool consumers":
		domain = readablename.Pool
	}
	if domain != "" {
		id, err := resolve(domain, args[0])
		if err != nil {
			return err
		}
		if id != args[0] {
			args = append([]string{}, args...)
			args[0] = id
			return f.Parse(append([]string{"--"}, args...))
		}
	}
	return nil
}

func hostAliasErrorPreserving(err error) error {
	if errors.Is(err, readablename.ErrNotFound) || errors.Is(err, readablename.ErrAmbiguous) || errors.Is(err, readablename.ErrIncomplete) {
		return hostAliasError(err)
	}
	return err
}
