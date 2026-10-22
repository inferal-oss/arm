package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

const LifecycleProtocol = 1

type SSHRoute struct {
	ClientIP  string `json:"client_ip"`
	ServerIP  string `json:"server_ip"`
	Synthetic bool   `json:"synthetic"`
}

func ParseSSHRoute(raw string) (SSHRoute, error) {
	fields := strings.Fields(raw)
	if len(fields) != 4 || net.ParseIP(fields[0]) == nil || net.ParseIP(fields[2]) == nil {
		return SSHRoute{}, fmt.Errorf("direct SSH connection did not report two literal interface addresses")
	}
	for _, index := range []int{1, 3} {
		port, err := strconv.Atoi(fields[index])
		if err != nil || port < 0 || port > 65535 || (index == 3 && port == 0) {
			return SSHRoute{}, fmt.Errorf("invalid SSH route port")
		}
	}
	return SSHRoute{ClientIP: net.ParseIP(fields[0]).String(), ServerIP: net.ParseIP(fields[2]).String(), Synthetic: fields[1] == "0"}, nil
}

func InterfaceIP(raw string) bool {
	want := net.ParseIP(raw)
	if want == nil || want.IsUnspecified() || want.IsMulticast() {
		return false
	}
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range interfaces {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.Equal(want) {
			return true
		}
	}
	return false
}

func AutomaticIP(raw string, allowPublic bool) error {
	ip := net.ParseIP(raw)
	if !InterfaceIP(raw) {
		return fmt.Errorf("mesh route IP is not assigned to this host; NAT/tunnels need an explicit verified endpoint")
	}
	if !ip.IsLoopback() && !ip.IsPrivate() && !allowPublic {
		return fmt.Errorf("mesh route uses a public/nonprivate interface; explicitly review --allow-public-listen instead of automatic exposure")
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("scoped/link-local mesh routes are not automatically supported")
	}
	return nil
}

type LifecycleInspection struct {
	Protocol      int            `json:"protocol"`
	Version       string         `json:"version"`
	OS            string         `json:"os"`
	Arch          string         `json:"arch"`
	Home          string         `json:"home"`
	Owner         local.Identity `json:"owner"`
	Running       bool           `json:"running"`
	Host          string         `json:"host,omitempty"`
	Identity      *Identity      `json:"identity,omitempty"`
	Config        *Config        `json:"config,omitempty"`
	Startup       StartupPolicy  `json:"startup"`
	StartupSource string         `json:"startup_source"`
	Route         *SSHRoute      `json:"ssh_route,omitempty"`
	PrivateIPs    []string       `json:"assigned_private_ips"`
}

type PrepareRequest struct {
	Key                 string `json:"key"`
	Name                string `json:"name"`
	IP                  string `json:"ip"`
	AllowPublic         bool   `json:"allow_public"`
	ExpectedHost        string `json:"expected_host,omitempty"`
	ExpectedFingerprint string `json:"expected_fingerprint,omitempty"`
}

type PreparedSetup struct {
	Key      string    `json:"key"`
	Token    string    `json:"token"`
	Identity Identity  `json:"identity"`
	Listen   string    `json:"listen"`
	Expires  time.Time `json:"expires_at"`
	Existing bool      `json:"existing_listener"`
}

type ApplySetup struct {
	Key         string `json:"key"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
	Listen      string `json:"listen"`
}

func DefaultStartup() StartupPolicy {
	return StartupPolicy{Schema: 1, Resources: model.Resources{CPU: float64(max(1, runtime.NumCPU()-1)), Disk: 0}, Listen: "127.0.0.1:0"}
}

func InspectLifecycle(home, version string) (LifecycleInspection, error) {
	if err := local.CheckStateFence(home); err != nil {
		return LifecycleInspection{}, err
	}
	inspection := LifecycleInspection{Protocol: LifecycleProtocol, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, Home: home, Owner: local.CurrentIdentity(), Startup: DefaultStartup(), StartupSource: "current defaults; no prior launch policy exists"}
	privateIPs, err := PrivateInterfaces()
	if err != nil {
		return inspection, err
	}
	inspection.PrivateIPs = privateIPs
	if raw := os.Getenv("SSH_CONNECTION"); raw != "" {
		route, err := ParseSSHRoute(raw)
		if err != nil {
			return inspection, err
		}
		inspection.Route = &route
	}
	policy, exists, err := ReadStartupPolicy(home)
	if err != nil {
		return inspection, err
	}
	if exists {
		inspection.Startup = policy
		inspection.StartupSource = "persisted successful backend launch"
	}
	raw, err := privateFile(filepath.Join(home, "mesh", "config.json"))
	if os.IsNotExist(err) {
		return inspection, nil
	}
	if err != nil {
		return inspection, err
	}
	var config Config
	if err := Decode(raw, &config); err != nil {
		return inspection, err
	}
	if _, err := parseIdentity(config.Identity, false); err != nil {
		return inspection, err
	}
	if err := validateListen(config.Listen); err != nil {
		return inspection, err
	}
	inspection.Host = config.Identity.Host
	inspection.Config = &config
	identity := config.Identity
	inspection.Identity = &identity
	return inspection, nil
}

func ReviewDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

type LifecycleTransition struct {
	Phase  string    `json:"phase"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}
type LifecycleOperation struct {
	Actor                  local.Identity        `json:"actor"`
	Transitions            []LifecycleTransition `json:"transitions"`
	Key                    string                `json:"key"`
	Kind                   string                `json:"kind"`
	Digest                 string                `json:"request_sha256"`
	At                     time.Time             `json:"at"`
	Updated                time.Time             `json:"updated_at"`
	Phase                  string                `json:"phase"`
	LocalHost              string                `json:"local_host,omitempty"`
	RemoteHost             string                `json:"remote_host,omitempty"`
	SSHLocator             string                `json:"ssh_locator,omitempty"`
	SSHAccount             string                `json:"ssh_account,omitempty"`
	SSHHostKey             string                `json:"ssh_host_key,omitempty"`
	RemoteHome             string                `json:"remote_home,omitempty"`
	Binary                 string                `json:"binary,omitempty"`
	Pair                   string                `json:"pair,omitempty"`
	ReviewedDigest         string                `json:"reviewed_sha256,omitempty"`
	LocalIdentity          *Identity             `json:"local_identity,omitempty"`
	RemoteIdentity         *Identity             `json:"remote_identity,omitempty"`
	RemoteRevision         int                   `json:"remote_revision,omitempty"`
	LocalRevision          int                   `json:"local_revision,omitempty"`
	ReviewedLocalRevision  int                   `json:"reviewed_local_revision"`
	ReviewedRemoteRevision int                   `json:"reviewed_remote_revision"`
	Reason                 string                `json:"reason"`
	Detail                 string                `json:"detail,omitempty"`
}

func LifecycleOperationPath(home, key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("lifecycle operation needs a nonempty key up to128 bytes")
	}
	name := sha256.Sum256([]byte(key))
	return filepath.Join(home, "mesh-lifecycle", "operation-"+hex.EncodeToString(name[:])+".json"), nil
}

func ReadLifecycleOperation(home, key string) (LifecycleOperation, bool, error) {
	var result LifecycleOperation
	path, err := LifecycleOperationPath(home, key)
	if err != nil {
		return result, false, err
	}
	raw, err := privateFile(path)
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if err := Decode(raw, &result); err != nil {
		return result, false, err
	}
	if result.Key != key {
		return result, false, fmt.Errorf("lifecycle key does not match durable record")
	}
	return result, true, nil
}

func SaveLifecycleOperation(home string, operation LifecycleOperation) error {
	path, err := LifecycleOperationPath(home, operation.Key)
	if err != nil {
		return err
	}
	if operation.Kind != "connect" && operation.Kind != "disconnect" || len(operation.Reason) == 0 || len(operation.Reason) > 4096 {
		return fmt.Errorf("invalid lifecycle operation")
	}
	previous, exists, err := ReadLifecycleOperation(home, operation.Key)
	if err != nil {
		return err
	}
	if exists {
		if previous.Kind != operation.Kind || previous.Digest != operation.Digest || previous.At != operation.At {
			return fmt.Errorf("lifecycle record key/intent conflicts with durable history")
		}
		operation.Actor = previous.Actor
		operation.Transitions = previous.Transitions
	} else {
		operation.Actor = local.CurrentIdentity()
		operation.Transitions = nil
	}
	operation.Updated = time.Now().UTC()
	if len(operation.Transitions) == 0 || operation.Transitions[len(operation.Transitions)-1].Phase != operation.Phase || operation.Transitions[len(operation.Transitions)-1].Detail != operation.Detail {
		if len(operation.Transitions) >= 128 {
			return fmt.Errorf("lifecycle transition history full; no audit record removed")
		}
		operation.Transitions = append(operation.Transitions, LifecycleTransition{Phase: operation.Phase, At: operation.Updated, Detail: operation.Detail})
	}
	parent, err := directory(home)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := unix.Mkdirat(int(parent.Fd()), "mesh-lifecycle", 0700); err != nil && err != unix.EEXIST {
		return err
	}
	dir, err := directory(filepath.Dir(path))
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(257)
	dir.Close()
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) >= 256 {
		if _, err := privateFile(path); os.IsNotExist(err) {
			return fmt.Errorf("mesh lifecycle history is full; no prior audit record was removed")
		}
	}
	return save(path, operation)
}

// LifecycleConnections is bounded and read-only; no state directory is created.
func LifecycleConnections(home string) ([]LifecycleOperation, error) {
	dir, err := directory(filepath.Join(home, "mesh-lifecycle"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(257)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 256 {
		return nil, fmt.Errorf("lifecycle history exceeds bound")
	}
	results := []LifecycleOperation{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "operation-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := privateFile(filepath.Join(home, "mesh-lifecycle", entry.Name()))
		if err != nil {
			return nil, err
		}
		var op LifecycleOperation
		if err = Decode(raw, &op); err != nil {
			return nil, err
		}
		if op.Kind == "connect" {
			results = append(results, op)
		}
	}
	return results, nil
}

// LockLifecycle serializes local operator orchestration, including durable
// exact-key intent updates. It never trusts a symlink or hardlinked lock file.
func LockLifecycle(home string) (func(), error) {
	dir, err := directory(home)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), "mesh-lifecycle.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0077 != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("unsafe lifecycle lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("another mesh lifecycle operation owns this ARM home: %w", err)
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}

func OpenLifecycleLog(home string) (*os.File, error) {
	dir, err := directory(home)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), "daemon.log", unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0077 != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("unsafe private backend startup log")
	}
	return os.NewFile(uintptr(fd), "daemon.log"), nil
}

// ResolveConfig preserves stable-ID precedence and local/name collision refusal
// for read-only operator workflows which must work with disabled listeners.
func ResolveConfig(config Config, target string) (Peer, error) { return resolve(config, target) }
