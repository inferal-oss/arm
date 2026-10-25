// Test fixture only: an unprivileged, fixed-exec SSH endpoint. It is never
// installed by ARM or accepted as a production node service.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"golang.org/x/crypto/ssh"
)

const inputLimit = 1 << 20
const outputLimit = 4 << 20

type options struct{ arm, home, hostKey, clientKey, ready, events, control, listen string }
type endpoint struct {
	options
	config     *ssh.ServerConfig
	account    string
	mu         sync.Mutex
	eventsFile *os.File
	sessions   chan struct{}
	requests   int
}
type packet struct {
	Action, Home string
	Pair         string
	Revoke       struct{ Key string }
}
type fault struct {
	Key   string `json:"key,omitempty"`
	Pair  string `json:"pair,omitempty"`
	Stage string `json:"stage"`
}
type observation struct {
	Sequence     int    `json:"sequence"`
	Action       string `json:"action"`
	Key          string `json:"key,omitempty"`
	Pair         string `json:"pair,omitempty"`
	InputSHA     string `json:"input_sha256"`
	ChildStarted bool   `json:"child_started"`
	WaitFailed   bool   `json:"child_wait_failed"`
	Exit         int    `json:"child_exit"`
	StdoutSHA    string `json:"stdout_sha256"`
	StderrSHA    string `json:"stderr_sha256"`
	Fault        string `json:"fault,omitempty"`
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func regularPrivate(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	before, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, fmt.Errorf("private bounded regular fixture file required")
	}
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("fixture input too large")
	}
	return raw, e
}
func imageDigest(path string, limit int64) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return "", e
	}
	before, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() < 1 || info.Size() > limit {
		return "", fmt.Errorf("private bounded image required")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, limit+1))
	if e != nil {
		return "", e
	}
	if n != info.Size() || n > limit {
		return "", fmt.Errorf("fixture image changed or too large")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func strictJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid fixture UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	members := 0
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("fixture JSON depth bound")
		}
		token, e := d.Token()
		if e != nil {
			return e
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			if text, ok := token.(string); ok && len(text) > 16384 {
				return fmt.Errorf("fixture scalar bound")
			}
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				name, ok := key.(string)
				if !ok || seen[name] || len(name) > 128 {
					return fmt.Errorf("duplicate/bounded fixture member required")
				}
				seen[name] = true
				members++
				if members > 1024 {
					return fmt.Errorf("fixture member bound")
				}
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return fmt.Errorf("fixture object framing")
			}
		case '[':
			for d.More() {
				members++
				if members > 1024 {
					return fmt.Errorf("fixture array bound")
				}
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return fmt.Errorf("fixture array framing")
			}
		default:
			return fmt.Errorf("fixture JSON framing")
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("one fixture JSON value required")
	}
	return nil
}

func parsePacket(raw []byte, home string) (packet, error) {
	var value map[string]json.RawMessage
	if len(raw) > inputLimit || strictJSON(raw) != nil || mesh.Decode(raw, &value) != nil {
		return packet{}, fmt.Errorf("bounded closed JSON packet required")
	}
	// Product validates each exact typed action. The fixture adds only a home
	// fence; it must not grant a client access to another real ARM authority.
	var p packet
	if json.Unmarshal(raw, &p) != nil || p.Home != home || p.Action == "" {
		return p, fmt.Errorf("exact fixture ARM home/action required")
	}
	return p, nil
}
func (s *endpoint) takeFault(p packet) (string, error) {
	if p.Action != "lifecycle_revoke" && p.Action != "pair" {
		return "", nil
	}
	raw, e := regularPrivate(s.control, 1024)
	if errors.Is(e, os.ErrNotExist) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	var f fault
	var fields map[string]json.RawMessage
	if strictJSON(raw) != nil || mesh.Decode(raw, &fields) != nil || len(fields) != 2 {
		return "", fmt.Errorf("closed exact-selector fixture fault required")
	}
	for key := range fields {
		if key != "key" && key != "pair" && key != "stage" {
			return "", fmt.Errorf("unexpected fixture fault field")
		}
	}
	if mesh.Decode(raw, &f) != nil || (f.Stage != "before" && f.Stage != "after") {
		return "", fmt.Errorf("invalid one-shot fixture fault")
	}
	// A control selects exactly one reviewed native action. No key/Pair
	// fallback or wildcard is permitted; malformed controls remain retained.
	if (f.Key == "") == (f.Pair == "") || (f.Key != "" && len(f.Key) > 128) {
		return "", fmt.Errorf("one exact bounded revoke key or Pair required")
	}
	if f.Pair != "" {
		if len(f.Pair) != 64 || strings.Trim(f.Pair, "0123456789abcdef") != "" {
			return "", fmt.Errorf("Pair must be exactly 64 lowercase hexadecimal characters")
		}
		if p.Action != "pair" || p.Pair != f.Pair {
			return "", nil
		}
	} else if p.Action != "lifecycle_revoke" || f.Key != p.Revoke.Key {
		return "", nil
	}
	if e = os.Remove(s.control); e != nil {
		return "", e
	}
	return f.Stage, nil
}

type capture struct {
	raw      []byte
	limit    int
	overflow bool
	mu       sync.Mutex
}

func (w *capture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := w.limit - len(w.raw)
	if len(p) > remaining {
		w.overflow = true
		p = p[:max(0, remaining)]
	}
	w.raw = append(w.raw, p...)
	return n, nil
}
func (s *endpoint) observe(v observation) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(raw) > 16384 {
		return fmt.Errorf("fixture event too large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e = s.eventsFile.Write(append(raw, '\n'))
	if e == nil {
		e = s.eventsFile.Sync()
	}
	return e
}

type fixedInvocation struct {
	action string
	argv   []string
	input  string
}

// Only these two commands are eligible: the selected ARM bootstrap and the
// product's exact fixed read-only probe for the same private image/home. Native
// argv always comes from this trusted builder, never from received shell text.
func (s *endpoint) fixedCommand(command string) (fixedInvocation, bool) {
	if command == s.arm+" mesh bootstrap" {
		return fixedInvocation{action: "bootstrap", argv: []string{s.arm, "mesh", "bootstrap"}}, true
	}
	probe, err := meshssh.FixedReadOnlyProbe(s.arm, s.home)
	if err != nil || command != strings.Join(probe.Remote, " ") {
		return fixedInvocation{}, false
	}
	return fixedInvocation{action: "read_only_probe", argv: probe.Argv, input: probe.Input}, true
}

func (s *endpoint) execute(ctx context.Context, c ssh.Channel, payload []byte, connection net.Conn) {
	defer c.Close()
	var request struct{ Command string }
	if ssh.Unmarshal(payload, &request) != nil {
		return
	}
	invocation, allowed := s.fixedCommand(request.Command)
	if !allowed {
		return
	}
	// Reject sessions beyond the fixed fixture budget before reading/launching.
	s.mu.Lock()
	s.requests++
	sequence := s.requests
	s.mu.Unlock()
	if sequence > 64 {
		return
	}
	raw, e := io.ReadAll(io.LimitReader(c, inputLimit+1))
	if e != nil {
		return
	}
	var p packet
	if invocation.action == "read_only_probe" {
		if string(raw) != invocation.input {
			return
		}
		p = packet{Action: invocation.action, Home: s.home}
	} else {
		p, e = parsePacket(raw, s.home)
		if e != nil {
			return
		}
	}
	s.mu.Lock()
	stage, e := s.takeFault(p)
	s.mu.Unlock()
	if e != nil {
		return
	}
	stdout, stderr := &capture{limit: outputLimit}, &capture{limit: 65536}
	event := observation{Sequence: sequence, Action: p.Action, Key: p.Revoke.Key, Pair: p.Pair, InputSHA: digest(raw), Exit: -1, Fault: stage}
	var waitErr error
	if stage != "before" {
		childCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		command := fixtureCommand(childCtx, invocation.argv[0], invocation.argv[1:]...)
		clientIP, clientPort, _ := net.SplitHostPort(connection.RemoteAddr().String())
		serverIP, serverPort, _ := net.SplitHostPort(connection.LocalAddr().String())
		command.Env = os.Environ()
		if invocation.action == "bootstrap" {
			command.Env = append(command.Env, "ARM_MESH_BOOTSTRAP=1", "SSH_CONNECTION="+strings.Join([]string{clientIP, clientPort, serverIP, serverPort}, " "))
		}
		command.Stdin = bytes.NewReader(raw)
		command.Stdout = stdout
		command.Stderr = stderr
		// The helper is one fixture-owned process. Ordinary ARM backends it starts
		// are separately enumerated/stopped by the harness, never inferred joined.
		if startErr := command.Start(); startErr == nil {
			event.ChildStarted = true
			waitErr = command.Wait()
			event.WaitFailed = waitErr != nil
			if command.ProcessState != nil {
				event.Exit = command.ProcessState.ExitCode()
			}
		}
		cancel()
	}
	event.StdoutSHA = digest(stdout.raw)
	event.StderrSHA = digest(stderr.raw)
	if observationErr := s.observe(event); observationErr != nil {
		return
	}
	status, complete := helperResponseStatus(event.ChildStarted, event.Exit, waitErr, stage != "" || stdout.overflow || stderr.overflow)
	if complete {
		_, _ = c.Write(stdout.raw)
		_, _ = c.Stderr().Write(stderr.raw)
	}
	_, _ = c.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
}

// Native exit and Wait completion are distinct facts. In particular a process
// can exit0 while Wait returns ErrWaitDelay for incomplete output, or have a
// negative ExitCode after a signal. Neither can produce a successful SSH reply.
func helperResponseStatus(started bool, exit int, waitErr error, forcedFailure bool) (status int, complete bool) {
	if started && exit == 0 && waitErr == nil && !forcedFailure {
		return 0, true
	}
	if forcedFailure || !started || exit <= 0 {
		return 91, false
	}
	return exit, false
}

// A scope owns every request/session goroutine through child Wait and output
// joining. Closing the connection's incoming channel stream cancels its
// remaining session work. Individual session Close is not a separate trigger.
// Native connection closure interrupts handshakes and reads on parent cancel.
type connectionScope struct {
	ctx        context.Context
	cancel     context.CancelFunc
	connection net.Conn
	joined     chan struct{}
	tasks      sync.WaitGroup
}

func newConnectionScope(parent context.Context, connection net.Conn) *connectionScope {
	ctx, cancel := context.WithCancel(parent)
	scope := &connectionScope{ctx: ctx, cancel: cancel, connection: connection, joined: make(chan struct{})}
	go func() { defer close(scope.joined); <-ctx.Done(); connection.Close() }()
	return scope
}
func (scope *connectionScope) launch(work func(context.Context)) {
	scope.tasks.Add(1)
	go func() { defer scope.tasks.Done(); work(scope.ctx) }()
}

// launch is called by the owning serve goroutine only, never after closeAndJoin.
func (scope *connectionScope) closeAndJoin() {
	scope.cancel()
	scope.connection.Close()
	scope.tasks.Wait()
	<-scope.joined
}
func fixtureCommand(ctx context.Context, image string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, image, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Keep CommandContext's retained Process.Kill: it refuses after native reap.
	// No raw saved-PID/group signal and no claim that separately started ARM
	// backends are joined; the public harness closes those explicitly.
	command.WaitDelay = 2 * time.Second
	return command
}

func validateListenAddress(address string) error {
	if len(address) > 32 {
		return fmt.Errorf("fixture requires a literal loopback address and numeric port")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || len(port) == 0 || len(port) > 5 {
		return fmt.Errorf("fixture requires 127.0.0.1 and a numeric port")
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("fixture requires a numeric port")
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n > 65535 {
		return fmt.Errorf("fixture port is outside 0..65535")
	}
	return nil
}

func (s *endpoint) serve(ctx context.Context, connection net.Conn) {
	scope := newConnectionScope(ctx, connection)
	defer scope.closeAndJoin()
	_ = connection.SetDeadline(time.Now().Add(35 * time.Second))
	server, channels, requests, e := ssh.NewServerConn(connection, s.config)
	if e != nil {
		return
	}
	defer server.Close()
	scope.launch(func(context.Context) { ssh.DiscardRequests(requests) })
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			incoming.Reject(ssh.UnknownChannelType, "fixture accepts fixed session only")
			continue
		}
		select {
		case s.sessions <- struct{}{}:
		default:
			incoming.Reject(ssh.ResourceShortage, "fixture session limit")
			continue
		}
		c, requests, e := incoming.Accept()
		if e != nil {
			<-s.sessions
			continue
		}
		scope.launch(func(sessionCtx context.Context) {
			defer func() { <-s.sessions }()
			defer c.Close()
			for request := range requests {
				switch request.Type {
				case "env":
					var env struct{ Name, Value string }
					ok := ssh.Unmarshal(request.Payload, &env) == nil && env.Name == "ARM_MESH_BOOTSTRAP" && env.Value == "1"
					request.Reply(ok, nil)
				case "exec":
					var v struct{ Command string }
					ok := ssh.Unmarshal(request.Payload, &v) == nil
					if ok {
						_, ok = s.fixedCommand(v.Command)
					}
					request.Reply(ok, nil)
					if ok {
						s.execute(sessionCtx, c, request.Payload, connection)
					}
					return
				default:
					request.Reply(false, nil)
				}
			}
		})
	}
}
func run(ctx context.Context, o options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := validateListenAddress(o.listen); err != nil {
		return err
	}
	for _, p := range []string{o.arm, o.home, o.hostKey, o.clientKey, o.ready, o.events, o.control} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("absolute canonical fixture paths required")
		}
	}
	if strings.ContainsAny(o.arm, " \t\r\n'\"\\") {
		return fmt.Errorf("fixed safe binary path required")
	}
	info, e := os.Lstat(o.arm)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private fixture image required")
	}
	hostRaw, e := regularPrivate(o.hostKey, 16384)
	if e != nil {
		return e
	}
	signer, e := ssh.ParsePrivateKey(hostRaw)
	if e != nil {
		return e
	}
	clientRaw, e := regularPrivate(o.clientKey, 16384)
	if e != nil {
		return e
	}
	key, _, _, rest, e := ssh.ParseAuthorizedKey(clientRaw)
	if e != nil || len(bytes.TrimSpace(rest)) != 0 {
		return fmt.Errorf("one exact fixture public key required")
	}
	account, e := user.Current()
	if e != nil {
		return e
	}
	events, e := os.OpenFile(o.events, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer events.Close()
	s := &endpoint{options: o, account: account.Username, sessions: make(chan struct{}, 2), eventsFile: events}
	s.config = &ssh.ServerConfig{MaxAuthTries: 1, PublicKeyCallback: func(metadata ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() != s.account || !bytes.Equal(public.Marshal(), key.Marshal()) {
			return nil, fmt.Errorf("fixture-only exact account/key")
		}
		return &ssh.Permissions{}, nil
	}}
	s.config.AddHostKey(signer)
	listener, e := net.Listen("tcp4", o.listen)
	if e != nil {
		return e
	}
	defer listener.Close()
	host, _, e := net.SplitHostPort(listener.Addr().String())
	if e != nil || host != "127.0.0.1" {
		return fmt.Errorf("fixture listens only on loopback")
	}
	imageSHA, e := imageDigest(o.arm, 512<<20)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(map[string]any{"address": listener.Addr().String(), "account": s.account, "server_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), "pid": os.Getpid(), "arm_sha256": imageSHA})
	if e = publishReady(o.ready, append(raw, '\n'), nil); e != nil {
		return e
	}
	watcherJoined := make(chan struct{})
	go func() {
		defer close(watcherJoined)
		<-ctx.Done()
		listener.Close()
	}()
	var wg sync.WaitGroup
	defer func() { cancel(); listener.Close(); wg.Wait(); <-watcherJoined }()
	connections := make(chan struct{}, 4)
	for {
		c, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case connections <- struct{}{}:
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-connections }(); s.serve(ctx, c) }()
		default:
			c.Close()
		}
	}
}
func main() {
	var o options
	flag.StringVar(&o.arm, "arm", "", "exact private executable")
	flag.StringVar(&o.home, "home", "", "exact disposable ARM home")
	flag.StringVar(&o.hostKey, "host-key", "", "fixture server private key")
	flag.StringVar(&o.clientKey, "client-public-key", "", "fixture-only client public key")
	flag.StringVar(&o.ready, "ready", "", "exclusive readiness file")
	flag.StringVar(&o.events, "events", "", "exclusive bounded event log")
	flag.StringVar(&o.control, "fault-control", "", "private one-shot fault file")
	flag.StringVar(&o.listen, "listen", "127.0.0.1:0", "loopback only")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if e := run(ctx, o); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
