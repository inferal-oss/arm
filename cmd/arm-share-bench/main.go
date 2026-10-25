//go:build arm_share_benchmark

// Private capability/performance harness, deliberately absent from normal ARM
// builds. No public grant, publication, admission or settlement is implemented.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharecodec"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/shareview"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

type options struct {
	mode, dir, address, cert, key, peer, sha, profile string
	mib, iterations, readBytes                        int
	timeout                                           time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.mode, "mode", "", "credentials, serve, raw, stream, mount or stop")
	flag.StringVar(&o.dir, "dir", "", "fresh private credential/source directory")
	flag.StringVar(&o.address, "address", "", "explicit private listen/dial address")
	flag.StringVar(&o.cert, "cert", "", "own certificate file")
	flag.StringVar(&o.key, "key", "", "own private key file")
	flag.StringVar(&o.peer, "peer", "", "exact peer public certificate file")
	flag.StringVar(&o.sha, "sha256", "", "expected source SHA256")
	flag.IntVar(&o.mib, "mib", 128, "synthetic data size MiB, 16..1024")
	flag.IntVar(&o.iterations, "iterations", 3, "fresh sessions per mode, 1..8")
	flag.DurationVar(&o.timeout, "timeout", 180*time.Second, "whole-process bound, <=10m")
	flag.IntVar(&o.readBytes, "read-bytes", 0, "mount application read bytes:0=ordinary io.Copy; otherwise4096..1048576")
	flag.StringVar(&o.profile, "cpuprofile", "", "fresh private CPU profile file; optional diagnostics")
	flag.Parse()
	if o.mib < 16 || o.mib > 1024 || o.iterations < 1 || o.iterations > 8 || o.timeout <= 0 || o.timeout > 10*time.Minute || o.readBytes != 0 && (o.readBytes < 4096 || o.readBytes > sharefs.ChunkBytes) {
		fatal(errors.New("invalid bounded benchmark options"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	if o.profile != "" && o.mode != "serve" {
		stop, err := beginProfile(o.profile)
		if err != nil {
			fatal(err)
		}
		defer stop()
	}

	var err error
	switch o.mode {
	case "credentials":
		err = credentials(o.dir)
	case "serve":
		err = serve(ctx, o)
	case "raw", "stream", "mount":
		err = measure(ctx, o)
	case "stop":
		var c *tls.Conn
		c, err = dial(ctx, o)
		if err == nil {
			_, err = c.Write([]byte{'x'})
			if err == nil {
				var b [1]byte
				_, err = io.ReadFull(c, b[:])
				if b[0] != 'x' {
					err = errors.New("stop not acknowledged")
				}
			}
			c.Close()
		}
	default:
		err = errors.New("explicit mode required")
	}
	if err != nil {
		fatal(err)
	}
}
func beginProfile(path string) (func(), error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute fresh CPU profile path required")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err = pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { pprof.StopCPUProfile(); f.Close() }, nil
}

func fatal(err error) {
	json.NewEncoder(os.Stderr).Encode(map[string]any{"error": err.Error()})
	os.Exit(1)
}
func emit(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fatal(err)
	}
}
func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func credentials(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("absolute fresh directory required")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	for _, name := range []string{"origin", "consumer"} {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		t := &x509.Certificate{SerialNumber: new(big.Int).SetBytes(pub[:16]), Subject: pkix.Name{CommonName: "arm-share-benchmark"}, DNSNames: []string{"arm-share-benchmark"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, t, t, pub, key)
		if err != nil {
			return err
		}
		priv, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		for suffix, data := range map[string][]byte{".crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ".key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})} {
			f, err := os.OpenFile(filepath.Join(dir, name+suffix), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, werr := f.Write(data)
			cerr := f.Close()
			if err = errors.Join(werr, cerr); err != nil {
				return err
			}
		}
	}
	return nil
}
func tlsConfig(o options, server bool) (*tls.Config, error) {
	st, err := os.Lstat(o.key)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return nil, errors.New("private regular0600 key required")
	}
	pair, err := tls.LoadX509KeyPair(o.cert, o.key)
	if err != nil {
		return nil, err
	}
	pemBytes, err := os.ReadFile(o.peer)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(pemBytes)
	if block == nil || len(rest) != 0 {
		return nil, errors.New("one exact peer certificate required")
	}
	peer, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(peer)
	c := &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, SessionTicketsDisabled: true, ServerName: "arm-share-benchmark", RootCAs: pool, ClientCAs: pool}
	if server {
		c.ClientAuth = tls.RequireAndVerifyClientCert
	}
	c.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.PeerCertificates) != 1 || !s.PeerCertificates[0].Equal(peer) {
			return errors.New("peer pin mismatch")
		}
		return nil
	}
	return c, nil
}
func dial(ctx context.Context, o options) (*tls.Conn, error) {
	c, err := tlsConfig(o, false)
	if err != nil {
		return nil, err
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: c}
	conn, err := d.DialContext(ctx, "tcp", o.address)
	if err != nil {
		return nil, err
	}
	tc := conn.(*tls.Conn)
	deadline, _ := ctx.Deadline()
	if err = tc.SetDeadline(deadline); err != nil {
		tc.Close()
		return nil, err
	}
	return tc, nil
}

func serve(ctx context.Context, o options) error {
	if !filepath.IsAbs(o.dir) {
		return errors.New("absolute fresh source directory required")
	}
	if err := os.Mkdir(o.dir, 0700); err != nil {
		return err
	}
	p, err := storage.Open(filepath.Join(o.dir, "managed"), nil)
	if err != nil {
		return err
	}
	defer p.Close()
	name := id()
	identity, err := p.Create(name)
	if err != nil {
		return err
	}
	a := model.Artifact{Name: name, Path: p.Path(name), Identity: &identity, Phase: "owned"}
	if err = os.Mkdir(filepath.Join(a.Path, "selected"), 0700); err != nil {
		return err
	}
	filename := filepath.Join(a.Path, "selected", "payload")
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	chunk := make([]byte, sharefs.ChunkBytes)
	if _, err = rand.Read(chunk); err != nil {
		f.Close()
		return err
	}
	hash := sha256.New()
	for range o.mib {
		if _, err = f.Write(chunk); err != nil {
			f.Close()
			return err
		}
		hash.Write(chunk)
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	e, err := sharefs.New(sharefs.DefaultLimits())
	if err != nil {
		return err
	}
	binding := sharefs.Binding{Authority: id(), Owner: uint32(os.Geteuid()), Storage: name, StorageRevision: 1, Share: id(), Generation: id()}
	m, err := e.Build(ctx, p, a, binding)
	if err != nil {
		return err
	}
	conf, err := tlsConfig(o, true)
	if err != nil {
		return err
	}
	raw, err := net.Listen("tcp", o.address)
	if err != nil {
		return err
	}
	defer raw.Close()
	stopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(stopCtx, func() { raw.Close() })
	defer stop()
	emit(map[string]any{"ready": true, "address": raw.Addr().String(), "bytes": int64(o.mib) << 20, "sha256": hex.EncodeToString(hash.Sum(nil)), "cache": "OS-warm: source generation+manifest hashed all bytes; fresh Origin/session caches per verified run", "tls": "1.3 mutual exact-cert pins plus ordinary certificate verification", "consumer_accounting": false})
	if o.profile != "" {
		stop, err := beginProfile(o.profile)
		if err != nil {
			return err
		}
		defer stop()
	}

	// Sequential sessions keep the origin global session bound explicit. Both
	// raw and verified modes use the same listener, cipher, file and TCP path.
	for {
		nc, err := raw.Accept()
		if err != nil {
			if stopCtx.Err() != nil {
				break
			}
			return err
		}
		tc := tls.Server(nc, conf)
		deadline, _ := ctx.Deadline()
		tc.SetDeadline(deadline)
		abort := func() { nc.Close() }
		after := context.AfterFunc(stopCtx, abort)
		if err = tc.HandshakeContext(stopCtx); err == nil {
			var mode [1]byte
			_, err = io.ReadFull(tc, mode[:])
			if err == nil {
				switch mode[0] {
				case 'x':
					_, err = tc.Write([]byte{'x'})
					cancel()
				case 'r':
					var file *os.File
					file, err = os.Open(filename)
					if err == nil {
						_, err = io.CopyBuffer(tc, file, make([]byte, sharefs.ChunkBytes))
						err = errors.Join(err, file.Close())
					}
				case 's':
					var origin *sharefs.Origin
					origin, err = e.Open(stopCtx, p, a, m, sharefs.Scope{Source: id(), EnrollmentRevision: 1, Consumer: id(), Session: id()}, func(c context.Context, _ sharefs.Access) error { return c.Err() })
					if err == nil {
						err = sharecodec.Serve(stopCtx, origin, tc, abort)
					}
				default:
					err = errors.New("invalid private benchmark operation")
				}
			}
		}
		after()
		tc.Close()
		abort()
		// EOF/connection-closed is normal SFTP end-of-session; no public session
		// or lease is released here. Serve itself retains its Origin drain duty.
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && stopCtx.Err() == nil {
			fmt.Fprintln(os.Stderr, "session:", err)
		}
	}
	emit(map[string]any{"stopped": true, "source_retained": o.dir, "consumer_accounting": false})
	return ctx.Err()
}

func measure(ctx context.Context, o options) error {
	want, err := hex.DecodeString(o.sha)
	if err != nil || len(want) != sha256.Size {
		return errors.New("exact expected SHA256 required")
	}
	for run := 1; run <= o.iterations; run++ {
		cpuBefore, _ := usage()
		c, err := dial(ctx, o)
		if err != nil {
			return err
		}
		mode := byte('r')
		if o.mode != "raw" {
			mode = 's'
		}
		if _, err = c.Write([]byte{mode}); err != nil {
			c.Close()
			return err
		}
		var n int64
		var digest []byte
		var elapsed time.Duration
		extra := map[string]any{}
		if o.mode == "raw" {
			h := sha256.New()
			start := time.Now()
			n, err = io.CopyBuffer(h, c, make([]byte, sharefs.ChunkBytes))
			elapsed = time.Since(start)
			digest = h.Sum(nil)
		} else {
			var client *sftp.Client
			client, err = sftp.NewClientPipe(c, c, sftp.MaxPacketUnchecked(sharecodec.MaxData), sftp.MaxConcurrentRequestsPerFile(sharecodec.Credits))
			if err == nil {
				if o.mode == "stream" {
					var f *sftp.File
					f, err = client.Open("/selected/payload")
					if err == nil {
						h := sha256.New()
						start := time.Now()
						n, err = io.Copy(h, f)
						elapsed = time.Since(start)
						digest = h.Sum(nil)
						err = errors.Join(err, f.Close())
					}
				} else {
					n, digest, elapsed, extra, err = mounted(ctx, client, o.readBytes)
				}
				err = errors.Join(err, client.Close())
			}
		}
		cerr := c.Close()
		if err != nil {
			return err
		}
		if cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			return cerr
		}
		if n != int64(o.mib)<<20 || !strings.EqualFold(hex.EncodeToString(digest), o.sha) {
			return errors.New("benchmark bytes/hash mismatch")
		}
		cpuAfter, rss := usage()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		emit(map[string]any{"mode": o.mode, "run": run, "bytes": n, "seconds": elapsed.Seconds(), "MiB_per_second": float64(n) / (1 << 20) / elapsed.Seconds(), "sha256": hex.EncodeToString(digest), "cpu_profile_enabled": o.profile != "", "process_cpu_seconds_for_session_including_setup_close": cpuAfter - cpuBefore, "process_peak_rss_bytes": rss, "heap_alloc": mem.HeapAlloc, "heap_sys": mem.HeapSys, "goarch": runtime.GOARCH, "goos": runtime.GOOS, "cpus": runtime.NumCPU(), "cache": "OS-warm origin; fresh client+Origin; FUSE direct I/O", "consumer_accounting": false, "adapter": extra})
	}
	return nil
}

// Benchmark-only client. Its only accepted peer is our exact pinned Origin
// codec. Generic SFTP metadata/data is not sufficient verification evidence and
// this wrapper is NOT a public consumer transport/parser security acceptance.
type remote struct {
	client *sftp.Client
	mu     sync.Mutex
	files  map[string]*sftp.File
}

func info(path string, i os.FileInfo) sharefs.Info {
	return sharefs.Info{Path: path, Directory: i.IsDir(), Size: i.Size(), Mode: uint32(i.Mode().Perm()), ModifiedSeconds: i.ModTime().Unix(), ModifiedNanos: 0}
}
func (r *remote) Stat(ctx context.Context, p string) (sharefs.Info, error) {
	if err := ctx.Err(); err != nil {
		return sharefs.Info{}, err
	}
	i, err := r.client.Stat("/" + p)
	if err != nil {
		return sharefs.Info{}, err
	}
	if err = ctx.Err(); err != nil {
		return sharefs.Info{}, err
	}
	v := info(p, i)
	if v.Directory {
		v.Size = 0
	}
	return v, nil
}
func (r *remote) List(ctx context.Context, p string, offset, limit int) ([]sharefs.Info, int, error) {
	if offset < 0 || limit < 1 || limit > 256 {
		return nil, 0, sharefs.ErrLimit
	}
	list, err := r.client.ReadDirContext(ctx, "/"+p)
	if err != nil {
		return nil, 0, err
	}
	if len(list) > sharefs.MaxEntries || offset > len(list) {
		return nil, 0, sharefs.ErrLimit
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	end := min(len(list), offset+limit)
	out := make([]sharefs.Info, 0, end-offset)
	for _, i := range list[offset:end] {
		name := i.Name()
		if name == "" || strings.Contains(name, "/") || shareview.ValidPath(name) != nil {
			return nil, 0, sharefs.ErrDenied
		}
		full := name
		if p != "" {
			full = p + "/" + name
		}
		v := info(full, i)
		if v.Directory {
			v.Size = 0
		}
		out = append(out, v)
	}
	return out, end, ctx.Err()
}
func (r *remote) OpenRead(ctx context.Context, p string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.files) >= 64 {
		return "", sharefs.ErrLimit
	}
	f, err := r.client.Open("/" + p)
	if err != nil {
		return "", err
	}
	h := id()
	r.files[h] = f
	return h, nil
}
func (r *remote) Read(ctx context.Context, h string, off int64, length int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if length < 0 || length > sharefs.ChunkBytes || off < 0 {
		return nil, sharefs.ErrLimit
	}
	r.mu.Lock()
	f := r.files[h]
	r.mu.Unlock()
	if f == nil {
		return nil, sharefs.ErrClosed
	}
	b := make([]byte, length)
	n, err := f.ReadAt(b, off)
	if cerr := ctx.Err(); cerr != nil {
		return nil, cerr
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b[:n], err
}
func (r *remote) CloseRead(h string) error {
	r.mu.Lock()
	f := r.files[h]
	r.mu.Unlock()
	if f == nil {
		return sharefs.ErrClosed
	}
	if err := f.Close(); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.files, h)
	r.mu.Unlock()
	return nil
}

func usage() (float64, int64) {
	var r unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &r) != nil {
		return 0, 0
	}
	rss := r.Maxrss
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6, rss
}
