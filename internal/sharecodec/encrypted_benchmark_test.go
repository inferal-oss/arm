package sharecodec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

// BenchmarkEncryptedWarmRead compares the verified codec and a raw TLS file
// stream on the SAME loopback pair, TLS version/cipher, file and warm page cache.
// Both endpoints run in this process. This is deliberately not a remote-network,
// cold-cache, mounted filesystem, authorization-ledger or durable-write result.
// Setup/publication/handshakes are excluded; per-read native identity and runtime
// guard checks remain enabled. No compression, socket/cache tuning or root work.
func BenchmarkEncryptedWarmRead(b *testing.B) {
	const total = 64 << 20
	p, err := storage.Open(filepath.Join(b.TempDir(), "managed"), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	name := strings.Repeat("a", 32)
	identity, err := p.Create(name)
	if err != nil {
		b.Fatal(err)
	}
	a := model.Artifact{Name: name, Path: p.Path(name), Phase: "owned", Identity: &identity}
	filename := filepath.Join(a.Path, "payload")
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		b.Fatal(err)
	}
	chunk := make([]byte, sharefs.ChunkBytes)
	if _, err = rand.Read(chunk); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < total/len(chunk); i++ {
		if _, err = f.Write(chunk); err != nil {
			b.Fatal(err)
		}
	}
	if err = f.Sync(); err != nil {
		b.Fatal(err)
	}
	if err = f.Close(); err != nil {
		b.Fatal(err)
	}
	engine, err := sharefs.New(sharefs.DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	binding := sharefs.Binding{Authority: strings.Repeat("b", 32), Owner: uint32(os.Geteuid()), Storage: name, StorageRevision: 1, Share: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}
	manifest, err := engine.Build(context.Background(), p, a, binding)
	if err != nil {
		b.Fatal(err)
	}
	// Build hashes all bytes, so both subcases intentionally measure warm cache.
	for _, mode := range []string{"raw-TLS13-warm", "codec-only-SFTP-TLS13-warm", "verified-SFTP-TLS13-warm"} {
		codec := mode != "raw-TLS13-warm"
		label := mode
		b.Run(label, func(b *testing.B) {
			server, client, abort := encryptedBenchmarkPair(b)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer abort()
			done := make(chan error, 1)
			var sftpClient *sftp.Client
			if mode == "codec-only-SFTP-TLS13-warm" {
				bridge, e := NewBridge(ctx, server, abort)
				if e != nil {
					b.Fatal(e)
				}
				h := benchmarkNativeHandler{filename}
				rs := sftp.NewRequestServer(bridge, sftp.Handlers{FileGet: h, FileList: h}, sftp.WithRSMaxTxPacket(MaxData))
				go func() { err := rs.Serve(); abort(); rs.Close(); bridge.Close(); done <- err }()
				sftpClient, err = sftp.NewClientPipe(client, client, sftp.MaxPacketUnchecked(MaxData), sftp.MaxConcurrentRequestsPerFile(Credits))
				if err != nil {
					b.Fatal(err)
				}
			} else if codec {
				origin, err := engine.Open(ctx, p, a, manifest, sharefs.Scope{Source: strings.Repeat("e", 32), EnrollmentRevision: 1, Consumer: strings.Repeat("f", 32), Session: strings.Repeat("1", 32)}, func(context.Context, sharefs.Access) error { return nil })
				if err != nil {
					b.Fatal(err)
				}
				go func() { done <- Serve(ctx, origin, server, abort) }()
				sftpClient, err = sftp.NewClientPipe(client, client, sftp.MaxPacketUnchecked(MaxData), sftp.MaxConcurrentRequestsPerFile(Credits))
				if err != nil {
					b.Fatal(err)
				}
			} else {
				go func() {
					file, err := os.Open(filename)
					if err != nil {
						done <- err
						return
					}
					defer file.Close()
					var request [1]byte
					for {
						if _, err = io.ReadFull(server, request[:]); err != nil {
							done <- err
							return
						}
						if _, err = file.Seek(0, 0); err != nil {
							done <- err
							return
						}
						if _, err = io.CopyN(server, file, total); err != nil {
							done <- err
							return
						}
					}
				}()
			}
			b.Cleanup(func() {
				cancel()
				abort()
				if sftpClient != nil {
					sftpClient.Close()
				}
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					b.Error("benchmark transport workers did not join")
				}
			})
			b.SetBytes(total)
			b.ReportAllocs()
			cpuStart := benchmarkCPU()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var n int64
				if codec {
					file, e := sftpClient.Open("/payload")
					if e != nil {
						b.Fatal(e)
					}
					n, err = io.Copy(io.Discard, file)
					closeErr := file.Close()
					if err == nil {
						err = closeErr
					}
				} else {
					if _, err = client.Write([]byte{1}); err != nil {
						b.Fatal(err)
					}
					n, err = io.CopyN(io.Discard, client, total)
				}
				if err != nil || n != total {
					b.Fatalf("read length%d: %v", n, err)
				}
			}
			b.StopTimer()
			cpu := benchmarkCPU() - cpuStart
			b.ReportMetric(cpu/(float64(total)*float64(b.N)/(1<<30)), "CPU-sec/GiB")
			b.Logf("TLS1.3 cipher=%s; loopback,64MiB warm managed file,8 codec credits,DATA<=%d, both endpoint CPU combined; authorization callback is a no-op fixture", tls.CipherSuiteName(client.ConnectionState().CipherSuite), MaxData)
		})
	}
}
func benchmarkCPU() float64 {
	var r unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &r) != nil {
		return 0
	}
	return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
}
func encryptedBenchmarkPair(b *testing.B) (*tls.Conn, *tls.Conn, func()) {
	b.Helper()
	makeCert := func(serial int64) tls.Certificate {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "private-share-benchmark"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
		if err != nil {
			b.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}
	}
	sc, cc := makeCert(1), makeCert(2)
	verify := func(expected []byte) func(tls.ConnectionState) error {
		return func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) != 1 || !bytes.Equal(s.PeerCertificates[0].Raw, expected) {
				return os.ErrPermission
			}
			return nil
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer listener.Close()
	clientRaw, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	serverRaw, err := listener.Accept()
	if err != nil {
		clientRaw.Close()
		b.Fatal(err)
	}
	abort := func() { serverRaw.Close(); clientRaw.Close() }
	server := tls.Server(serverRaw, &tls.Config{Certificates: []tls.Certificate{sc}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert, VerifyConnection: verify(cc.Certificate[0])})
	client := tls.Client(clientRaw, &tls.Config{Certificates: []tls.Certificate{cc}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, InsecureSkipVerify: true, VerifyConnection: verify(sc.Certificate[0])})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.HandshakeContext(ctx) }()
	if err = client.HandshakeContext(ctx); err != nil {
		abort()
		b.Fatal(err)
	}
	if err = <-done; err != nil {
		abort()
		b.Fatal(err)
	}
	return server, client, abort
}

// benchmarkNativeHandler isolates the exact same bounded codec's overhead. It
// serves only this private fixture; it deliberately has no production authority.
type benchmarkNativeHandler struct{ filename string }

func (h benchmarkNativeHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if r.Filepath != "/payload" || r.Flags != 1 {
		return nil, os.ErrPermission
	}
	return os.Open(h.filename)
}
func (h benchmarkNativeHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if r.Filepath != "/payload" || r.Method != "Stat" {
		return nil, os.ErrPermission
	}
	v, err := os.Stat(h.filename)
	if err != nil {
		return nil, err
	}
	return benchmarkLister{v}, nil
}

type benchmarkLister []os.FileInfo

func (l benchmarkLister) ListAt(dst []os.FileInfo, off int64) (int, error) {
	if off < 0 {
		return 0, os.ErrInvalid
	}
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	if int(off)+n >= len(l) {
		return n, io.EOF
	}
	return n, nil
}
