package sshservice

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Window struct{ Columns, Rows, WidthPixels, HeightPixels uint32 }
type Terminal struct {
	Name   string
	Window Window
	Modes  ssh.TerminalModes
}
type Execution struct {
	Kind, Command string
	Terminal      *Terminal
}
type Exit struct {
	Code   uint32
	Signal string
}

// On successful Execute, Process represents a NEW destination-owned lease and
// process group. A nonnil Process returned with an error carries cleanup only:
// Serve retains it for JoinTransport, never accepts it or calls execution methods.
// The execution adapter owns authorization and native-process cleanup.
type Process interface {
	Wait(context.Context) (Exit, error)
	Signal(context.Context, string) error
	Resize(context.Context, Window) error
	// JoinTransport is called only after connection/channel shutdown and all
	// protocol/session/Wait workers returned. It joins the adapter's actual IO
	// and cleanup callbacks, never the managed native job's completion.
	JoinTransport()
}

type sessionStartTimer struct {
	timer   *time.Timer
	done    chan struct{}
	once    sync.Once
	stopped bool
}

func newSessionStartTimer(duration time.Duration, closeConnection func()) *sessionStartTimer {
	owner := &sessionStartTimer{done: make(chan struct{})}
	owner.timer = time.AfterFunc(duration, func() { defer close(owner.done); closeConnection() })
	return owner
}
func (owner *sessionStartTimer) stop() bool {
	owner.once.Do(func() {
		owner.stopped = owner.timer.Stop()
		if owner.stopped {
			close(owner.done)
		}
	})
	return owner.stopped
}
func (owner *sessionStartTimer) join() { owner.stop(); <-owner.done }

type Permit struct {
	Account   string
	PublicKey ssh.PublicKey
}
type Server struct {
	HostKey                 ssh.Signer
	Revalidate              func(context.Context) error
	Execute                 func(context.Context, Execution, ssh.Channel) (Process, error)
	Handshake, StartTimeout time.Duration
}

// Serve never launches a process itself. Execute is called at most once after
// public-key possession and a final authorization check, and must atomically
// create/consume the execution admission and its fresh lease before launch.
func (s Server) Serve(ctx context.Context, c net.Conn, p Permit) error {
	if s.HostKey == nil || s.HostKey.PublicKey().Type() != ssh.KeyAlgoED25519 || p.PublicKey == nil || p.PublicKey.Type() != ssh.KeyAlgoED25519 || !validAccount(p.Account) || s.Revalidate == nil || s.Execute == nil || s.Handshake <= 0 || s.Handshake > 30*time.Second || s.StartTimeout <= 0 || s.StartTimeout > 30*time.Second {
		return fmt.Errorf("invalid SSH service boundary")
	}
	if err := s.Revalidate(ctx); err != nil {
		return err
	}
	closeDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(closeDone); _ = c.Close() })
	defer func() {
		if !stop() {
			<-closeDone
		}
	}()
	defer c.Close()
	deadline := time.Now().Add(s.Handshake)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.SetDeadline(deadline); err != nil {
		return err
	}
	config := &ssh.ServerConfig{MaxAuthTries: 2, PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() != p.Account || key.Type() != ssh.KeyAlgoED25519 || !bytes.Equal(key.Marshal(), p.PublicKey.Marshal()) || s.Revalidate(ctx) != nil {
			return nil, fmt.Errorf("SSH invocation authentication denied")
		}
		return &ssh.Permissions{}, nil
	}}
	config.AddHostKey(s.HostKey)
	conn, channels, requests, err := ssh.NewServerConn(c, config)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	connectionDone := make(chan struct{})
	go func() { defer close(connectionDone); _ = conn.Wait(); cancel() }()
	// A successful handshake has started transport workers. Own their actual
	// completion before the first subsequent fallible operation (deadline clear).
	defer func() { cancel(); _ = conn.Close(); <-connectionDone }()
	if err = c.SetDeadline(time.Time{}); err != nil {
		return err
	}
	var workers sync.WaitGroup
	joiners := make(chan Process, 1) // one admitted execution, published before Wait starts
	start := newSessionStartTimer(s.StartTimeout, func() { _ = conn.Close() })
	defer func() {
		cancel()
		_ = conn.Close()
		start.join()
		<-connectionDone
		workers.Wait()
		select {
		case process := <-joiners:
			process.JoinTransport()
		default:
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-runCtx.Done():
				return
			case req, ok := <-requests:
				if !ok {
					return
				}
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}
	}()
	seen := false
	for {
		select {
		case <-runCtx.Done():
			return runCtx.Err()
		case next, ok := <-channels:
			if !ok {
				return nil
			}
			if seen || next.ChannelType() != "session" || len(next.ExtraData()) != 0 {
				_ = next.Reject(ssh.Prohibited, "one execution session only")
				continue
			}
			seen = true
			channel, reqs, err := next.Accept()
			if err != nil {
				return err
			}
			// Keep the main channel loop alive to reject additional channels while the
			// sole session executes. Completion closes the entire admitted connection.
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer cancel()
				defer conn.Close()
				s.session(runCtx, channel, reqs, start, &workers, joiners)
				closeSession(runCtx, channel, reqs)
			}()
		}
	}
}
func (s Server) session(ctx context.Context, ch ssh.Channel, requests <-chan *ssh.Request, start *sessionStartTimer, workers *sync.WaitGroup, joiners chan<- Process) {
	var terminal *Terminal
	var process Process
	var finished <-chan Exit
	for {
		select {
		case <-ctx.Done():
			return
		case exit := <-finished:
			if exit.Signal != "" && validSignal(exit.Signal) {
				_ = sendExitSignal(ch, exit.Signal)
			} else {
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exit.Code}))
			}
			_ = ch.CloseWrite()
			return
		case req, ok := <-requests:
			if !ok {
				return
			}
			accepted := false
			switch req.Type {
			case "pty-req":
				if process == nil && terminal == nil {
					v, err := parseTerminal(req.Payload)
					if err == nil {
						terminal = &v
						accepted = true
					}
				}
			case "window-change":
				var v struct{ Columns, Rows, WidthPixels, HeightPixels uint32 }
				if terminal != nil && ssh.Unmarshal(req.Payload, &v) == nil {
					w := Window{v.Columns, v.Rows, v.WidthPixels, v.HeightPixels}
					if validWindow(w) {
						if process == nil {
							terminal.Window = w
							accepted = true
						} else {
							accepted = process.Resize(ctx, w) == nil
						}
					}
				}
			case "exec", "shell":
				if process == nil {
					execution := Execution{Kind: req.Type, Terminal: terminal}
					valid := len(req.Payload) == 0 && req.Type == "shell"
					if req.Type == "exec" {
						var v struct{ Command string }
						if ssh.Unmarshal(req.Payload, &v) == nil && len(v.Command) > 0 && len(v.Command) <= 32768 && !strings.ContainsRune(v.Command, 0) {
							execution.Command = v.Command
							valid = true
						}
					}
					if valid && s.Revalidate(ctx) == nil {
						// Stop the waiting-for-execution timer before the synchronous adapter;
						// the stream's independent lifetime/cancellation still bounds it.
						if !start.stop() || ctx.Err() != nil {
							return
						}
						started, err := s.Execute(ctx, execution, ch)
						if started != nil {
							joiners <- started // includes cleanup-only failed admissions
						}
						if err == nil && started != nil {
							process = started
							accepted = true
							done := make(chan Exit, 1)
							finished = done
							workers.Add(1) // session worker remains owned while adding its child
							go func() {
								defer workers.Done()
								exit, err := started.Wait(ctx)
								if err != nil {
									exit = Exit{Code: 255}
								}
								done <- exit
							}()
						} else {
							if req.WantReply {
								_ = req.Reply(false, nil)
							}
							return
						}
					}
				}
			case "signal":
				var v struct{ Signal string }
				if process != nil && ssh.Unmarshal(req.Payload, &v) == nil && validSignal(v.Signal) {
					accepted = process.Signal(ctx, v.Signal) == nil
				}
			}
			if req.WantReply {
				_ = req.Reply(accepted, nil)
			}
		}
	}
}
func validSignal(s string) bool {
	switch s {
	case "HUP", "INT", "QUIT", "TERM", "KILL", "USR1", "USR2", "WINCH":
		return true
	}
	return false
}
func validWindow(w Window) bool {
	return w.Columns >= 1 && w.Columns <= 1000 && w.Rows >= 1 && w.Rows <= 1000 && w.WidthPixels <= 16384 && w.HeightPixels <= 16384
}
func parseTerminal(raw []byte) (Terminal, error) {
	var v struct {
		Name                                     string
		Columns, Rows, WidthPixels, HeightPixels uint32
		Modes                                    string
	}
	var out Terminal
	if len(raw) > 2048 || ssh.Unmarshal(raw, &v) != nil || len(v.Name) == 0 || len(v.Name) > 64 {
		return out, fmt.Errorf("invalid PTY request")
	}
	for _, r := range v.Name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r)) {
			return out, fmt.Errorf("invalid TERM")
		}
	}
	out = Terminal{Name: v.Name, Window: Window{v.Columns, v.Rows, v.WidthPixels, v.HeightPixels}, Modes: ssh.TerminalModes{}}
	if !validWindow(out.Window) || len(v.Modes) > 1024 {
		return out, fmt.Errorf("invalid PTY dimensions/modes")
	}
	p := wireParser{raw: []byte(v.Modes)}
	for len(p.raw) > 0 {
		opcode := p.raw[0]
		p.raw = p.raw[1:]
		if opcode == 0 {
			if len(p.raw) != 0 {
				return out, fmt.Errorf("trailing terminal modes")
			}
			return out, nil
		}
		if !validMode(opcode) || len(p.raw) < 4 {
			return out, fmt.Errorf("unsupported terminal mode")
		}
		if _, exists := out.Modes[opcode]; exists {
			return out, fmt.Errorf("duplicate terminal mode")
		}
		out.Modes[opcode] = uint32(p.raw[0])<<24 | uint32(p.raw[1])<<16 | uint32(p.raw[2])<<8 | uint32(p.raw[3])
		p.raw = p.raw[4:]
	}
	return out, fmt.Errorf("terminal modes terminator required")
}
func validMode(n byte) bool {
	return n >= 1 && n <= 18 || n >= 30 && n <= 42 || n >= 50 && n <= 62 || n >= 70 && n <= 75 || n >= 90 && n <= 93 || n == 128 || n == 129
}
func sendExitSignal(ch ssh.Channel, signal string) error {
	_, err := ch.SendRequest("exit-signal", false, ssh.Marshal(struct {
		Signal            string
		Core              bool
		Message, Language string
	}{signal, false, "", ""}))
	return err
}

// Give the peer a bounded chance to receive exit-status and acknowledge the
// channel close before destroying its transport. Closing immediately races
// queued SSH control packets (observed as native OpenSSH exit255 on Linux).
func closeSession(ctx context.Context, ch ssh.Channel, requests <-chan *ssh.Request) {
	_ = ch.Close()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case req, ok := <-requests:
			if !ok {
				return
			}
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}
