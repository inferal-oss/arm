package cli

import (
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/companions"
	"github.com/yrashk/arm/internal/local"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
)

// provisionMissingMesh is the native CLI's separate install/start review. The
// shared operator/browser prepare does not silently inherit upload authority.
func (a *app) provisionMissingMesh(key string, request *meshConnectRequest, artifactPath, installRoot string, yes, dry bool) (bool, error) {
	probe, sshIdentity, e := meshssh.ProbeHost(a.ctx, request.Destination, request.Binary, request.RemoteHome, meshssh.Identity{})
	if e != nil {
		return false, e
	}
	if probe.Installed != "" {
		return false, nil
	}
	var selected mesh.Artifact
	var source *companions.Catalog
	if artifactPath == "" {
		var closer interface{ Close() error }
		source, closer, e = a.meshCompanionSource()
		if e != nil {
			return false, e
		}
		if closer != nil {
			defer closer.Close()
		}
		if source != nil {
			payload, ok := source.Lookup(probe.OS, probe.Arch)
			if !ok {
				return false, model.Err("artifact_unavailable", "reviewed catalog does not contain target platform")
			}
			manifest := source.Manifest()
			selected = mesh.Artifact{Schema: 1, SHA256: payload.SHA256, Size: payload.Size, OS: payload.OS, Arch: payload.Arch, Version: manifest.Version, Protocol: manifest.Protocol, Imported: time.Now().UTC(), Actor: local.CurrentIdentity(), Reason: request.Reason}
		}
	}
	if artifactPath == "" && source == nil {
		artifacts, e := mesh.Artifacts(a.home)
		if e != nil {
			return false, e
		}
		for _, artifact := range artifacts {
			if artifact.OS == probe.OS && artifact.Arch == probe.Arch {
				if selected.SHA256 != "" {
					return false, model.Err("artifact_ambiguous", "multiple trusted artifacts match; select an exact local --artifact file")
				}
				selected = artifact
			}
		}
		if selected.SHA256 == "" {
			executable, e := os.Executable()
			if e != nil {
				return false, e
			}
			companion := filepath.Join(filepath.Dir(executable), "arm-"+probe.OS+"-"+probe.Arch)
			if probe.OS == runtime.GOOS && probe.Arch == runtime.GOARCH {
				companion = executable
			}
			if _, e = os.Stat(companion); e == nil {
				artifactPath = companion
			}
		}
	}
	if artifactPath != "" {
		selected, e = mesh.InspectArtifact(artifactPath, request.Reason)
		if e != nil {
			return false, e
		}
	}
	if selected.SHA256 == "" {
		return false, model.Err("artifact_unavailable", fmt.Sprintf("target has no ARM; supply a trusted CGo-free %s/%s companion with --artifact FILE or mesh artifacts import FILE; no release download is configured", probe.OS, probe.Arch))
	}
	if selected.OS != probe.OS || selected.Arch != probe.Arch {
		return false, model.Err("artifact_platform_mismatch", "selected companion does not match verified target OS/architecture")
	}
	if installRoot == "" {
		installRoot = filepath.Join(probe.Home, ".local", "share", "arm", "provisioned")
	}
	startup, e := meshops.ProvisionStartup(probe)
	if e != nil {
		return false, e
	}
	transfer := meshssh.Transfer{Root: installRoot, Key: meshops.ProvisionKey(key), SHA256: selected.SHA256, Size: selected.Size, ExpectedUID: probe.UID, OS: probe.OS, Arch: probe.Arch, Version: selected.Version, Protocol: selected.Protocol, Reason: request.Reason}
	q := meshops.ProvisionRequest{ConnectionKey: key, Destination: request.Destination, Transfer: transfer, SSH: sshIdentity, Home: probe.StateHome, Startup: startup}
	if source != nil {
		manifest := source.Manifest()
		_, digest, err := companions.Canonical(manifest)
		if err != nil {
			return false, err
		}
		q.Seed = &meshssh.CatalogSeed{Schema: 1, Digest: digest, Manifest: manifest}
	}
	if e = q.Validate(); e != nil {
		return false, e
	}
	coordinator := a.meshCoordinator()
	if source != nil {
		coordinator.ProvisionCatalog = source
		coordinator.ProvisionTransport = &meshops.ProvisionTransport{Import: func(expected mesh.Artifact) (mesh.Artifact, error) {
			stream, _, err := source.Open(expected.OS, expected.Arch)
			if err != nil {
				return expected, err
			}
			defer stream.Close()
			return mesh.ImportCompanion(a.home, expected, stream)
		}}
	}
	if artifactPath != "" {
		coordinator.ProvisionTransport = &meshops.ProvisionTransport{Import: func(mesh.Artifact) (mesh.Artifact, error) {
			return mesh.ImportArtifact(a.home, artifactPath, request.Reason)
		}}
	}
	var output any
	coordinator.Print = func(value any) error { output = value; return nil }
	code, e := coordinator.Provision(q, selected, yes, dry)
	if dry || code != 0 || e != nil {
		if output != nil {
			if pe := a.print(output); pe != nil {
				return true, pe
			}
		}
		return true, e
	}
	encoded, e := json.Marshal(output)
	if e != nil {
		return true, e
	}
	var result meshops.ProvisionResult
	if e = json.Unmarshal(encoded, &result); e != nil || result.Provisioned == "" {
		return true, fmt.Errorf("invalid exact provision result")
	}
	request.Binary = result.Provisioned
	request.RemoteHome = q.Home
	request.ProvisionKey = result.Key
	return false, nil
}
