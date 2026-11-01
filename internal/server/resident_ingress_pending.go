package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentservice"
)

const residentIngressConnectionLimit = 8
const residentIngressRequestsPerConnection = 8 // actual existing s.ws request channel
const residentIngressResponseQueue = 64        // actual existing original sink.out
const residentControlResponseBytes = 16 << 10  // FULL encoded envelope
const residentControlRequestBytes = 64 << 10   // existing full WS input limit

// Compile-time unsigned arithmetic cannot silently wrap. These are logical
// application floors; preexisting/native/runtime/allocator/driver costs remain
// additional measured standing-profile obligations, not an RSS/native census.
func residentIngressRequiredBounds() residentservice.Bounds {
	const requestSlots = residentIngressConnectionLimit * residentIngressRequestsPerConnection
	const queuedAndWriting = residentIngressConnectionLimit * (residentIngressResponseQueue + 1)
	// Each reader can hold another decoded request while all eight producers
	// remain live. Reserve both its input/decoding and original/prepared copies.
	const requestCopies = 2 * (requestSlots + residentIngressConnectionLimit) * residentControlRequestBytes
	const responseCopies = (queuedAndWriting + 2*requestSlots) * residentControlResponseBytes
	// Start payload, publisher-owned payload, and canonical validation copy.
	const publicationCopies = 3 * residentrefs.MaxResidentControlBytes
	const payloadBytes = requestCopies + responseCopies + publicationCopies
	return residentservice.Bounds{
		Tasks:                1 + 9 + 1 + residentIngressConnectionLimit*(1+1+residentIngressRequestsPerConnection),
		Descriptors:          13 + 1 + residentIngressConnectionLimit + 1,
		Children:             0, // K0 creates no additional native child; original profile stays positive
		Sockets:              1 + 1 + residentIngressConnectionLimit,
		NativePending:        1 + 9 + 1 + residentIngressConnectionLimit*(1+1+residentIngressRequestsPerConnection),
		IOBytes:              payloadBytes,
		RetainedReceiptBytes: payloadBytes,
	}
}

type residentIngressStartCall struct {
	token                                                                     [32]byte
	entropyAttempted, entropyReturned                                         bool
	entropyBytes                                                              int
	entropyError                                                              error
	listen, infrastructure, publicationConstruction, publication, serveLaunch residentProtocolResult
	publicationPayload                                                        []byte
	listenConfig                                                              *net.ListenConfig
	normalReturned                                                            bool
	err                                                                       error
	abnormal                                                                  bool
	cause                                                                     any
}
type residentIngressServeCall struct {
	registered, entered, returned, abnormal bool
	err                                     error
	cause                                   any
	parkAttempted, parkReturned             bool
}

// Exact original listener/control/runtime ownership. Errors retain originals;
// no cleanup path cancels the root, closes its Manager or releases promises.
type residentIngressPending struct {
	mu          sync.Mutex
	self        *residentIngressPending
	root        *residentChildPending
	runtime     *residentRuntimeCall
	server      *residentServerPending
	ctx         context.Context
	start       *residentIngressStartCall
	listener    net.Listener
	bounded     *residentIngressListener
	publication *residentrefs.ResidentControlPublicationPending
	httpServer  *http.Server
	serve       *residentIngressServeCall
	slots       [residentIngressConnectionLimit]*residentIngressConnection
	lastSettled [residentIngressConnectionLimit]*residentIngressConnection
	available   chan int
	firstError  error
}

func (o *residentServerPending) newOriginalIngress(root *residentChildPending) (*residentIngressPending, error) {
	if o == nil || o.self != o || root == nil || root.self != root || root.server != o || root.runtimeCall == nil {
		return nil, errResidentServerOriginal
	}
	c := root.runtimeCall
	if err := root.runtimeGate(c); err != nil {
		return nil, err
	}
	if !c.managerStart.completeData() || !c.ingressConstruction.attempted || c.ingressConstruction.returned {
		return nil, errResidentServerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ingress != nil || o.active != nil || o.firstError != nil || !o.started || !o.startTried || !o.opened || o.manager != c.manager || o.server == nil || o.server.resident != o || o.server.Manager == nil || o.binding == nil || o.binding.server != o.server {
		return nil, errResidentServerOriginal
	}
	o.ingress = &residentIngressPending{root: root, runtime: c, server: o, ctx: root.rootContext}
	p := o.ingress // original field retained before subsequent initialization
	p.self = p
	p.available = make(chan int, residentIngressConnectionLimit)
	for i := range p.slots {
		p.available <- i
	}
	return p, nil
}

func (p *residentIngressPending) original() bool {
	if p == nil || p.self != p || p.root == nil || p.root.self != p.root || p.root.mu == nil || p.server == nil || p.server.self != p.server || p.ctx == nil {
		return false
	}
	r := p.root
	r.mu.Lock()
	c := r.runtimeCall
	valid := c != nil && c == p.runtime && c.owner == r && c.server == p.server && c.manager == r.manager && c.charge == r.charge && c.ingress == p && c.managerStart.completeData() && c.ingressConstruction.completeData() && c.op != nil && !c.op.abnormal && (r.active == c.op || c.op.normalFinished && c.op.finalError == nil && c.bodyReturned && c.bodyError == nil)
	r.mu.Unlock()
	p.server.mu.Lock()
	valid = valid && p.server.ingress == p && p.server.server != nil && p.server.server.resident == p.server && p.server.started && p.server.server.Manager != nil
	p.server.mu.Unlock()
	return valid
}

func (p *residentIngressPending) finishStart(c *residentIngressStartCall, normal *bool, resultErr *error) {
	cause := recover()
	p.mu.Lock()
	if p.start != c {
		p.mu.Unlock()
		panic("original resident ingress start lost")
	}
	c.normalReturned, c.err, c.abnormal, c.cause = *normal, *resultErr, !*normal, cause
	if !*normal && p.firstError == nil {
		p.firstError = &residentServerAbnormal{cause: cause}
	}
	if *resultErr != nil && p.firstError == nil {
		p.firstError = *resultErr
	}
	p.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (p *residentIngressPending) StartOriginal() (resultErr error) {
	if !p.original() {
		return errResidentServerOriginal
	}
	p.mu.Lock()
	if p.start != nil || p.firstError != nil {
		p.mu.Unlock()
		return errResidentServerOriginal
	}
	c := &residentIngressStartCall{}
	p.start = c
	p.mu.Unlock()
	normal := false
	defer p.finishStart(c, &normal, &resultErr)
	resultErr = p.startBody(c)
	normal = true
	return resultErr
}

func (p *residentIngressPending) startBody(c *residentIngressStartCall) error {
	if err := p.root.runtimeGate(p.runtime); err != nil {
		return err
	}
	if err := p.server.validateIngressBinding(p.ctx, p.server.binding, p.server.server); err != nil {
		return err
	}
	settings := p.root.bootstrap.Settings
	host, _, err := net.SplitHostPort(settings.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return model.Err("invalid_address", "resident control requires literal loopback")
	}
	c.entropyAttempted = true
	c.entropyBytes, c.entropyError = rand.Read(c.token[:])
	c.entropyReturned = true
	if c.entropyError != nil {
		return c.entropyError
	}
	if c.entropyBytes != len(c.token) {
		return errResidentServerOriginal
	}
	c.listen.attempted = true
	c.listenConfig = &net.ListenConfig{}
	p.listener, c.listen.err = c.listenConfig.Listen(p.ctx, "tcp", settings.Listen)
	c.listen.returned = true
	if c.listen.err != nil {
		return c.listen.err
	}
	if p.listener == nil {
		return errResidentServerOriginal
	}
	c.infrastructure.attempted = true
	c.infrastructure.err = p.server.server.Manager.TrackInfrastructure(p.listener.Addr())
	c.infrastructure.returned = true
	if c.infrastructure.err != nil {
		return c.infrastructure.err
	}
	s := p.server.server
	s.authority = p.listener.Addr().String()
	s.token = hex.EncodeToString(c.token[:])
	s.lifecycleContext = p.ctx // independent original root, never HTTP/worker context
	s.runtimeExecutable = p.root.runtime
	ep := local.Endpoint{Generation: s.generation, URL: "ws://" + s.authority + "/v1/ws", Token: s.token, PID: os.Getpid()}
	c.publicationPayload, err = json.Marshal(ep)
	if err != nil {
		return err
	}
	c.publicationConstruction.attempted = true
	p.publication, c.publicationConstruction.err = residentrefs.NewOriginalResidentControlPublication(p.root.serviceCustody)
	c.publicationConstruction.returned = true
	if c.publicationConstruction.err != nil {
		return c.publicationConstruction.err
	}
	c.publication.attempted = true
	c.publication.err = p.publication.PublishOriginal(p.ctx, c.publicationPayload)
	c.publication.returned = true
	if c.publication.err != nil {
		return c.publication.err
	}
	p.bounded = &residentIngressListener{owner: p, original: p.listener}
	p.httpServer = &http.Server{Handler: http.HandlerFunc(p.serveControl), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return p.ctx }, ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
		owned, ok := conn.(*residentIngressConnection)
		if !ok || owned.owner != p {
			return ctx
		}
		return context.WithValue(ctx, residentIngressConnectionKey{}, owned)
	}}
	p.httpServer.SetKeepAlivesEnabled(false) // one original control handler per slot
	p.serve = &residentIngressServeCall{registered: true}
	c.serveLaunch.attempted = true
	go p.runServeOriginal(p.serve)
	c.serveLaunch.returned = true
	return p.ctx.Err() // launch observation only; never API/UI readiness
}

func (p *residentIngressPending) runServeOriginal(c *residentIngressServeCall) {
	normal := false
	var err error
	defer p.finishServe(c, &normal, &err)
	p.mu.Lock()
	c.entered = true
	p.mu.Unlock()
	err = p.httpServer.Serve(p.bounded)
	normal = true
}

// Direct original goroutine defer. Abnormal/Goexit retains THIS same frame;
// no caretaker, root cancellation, Manager close or fake native completion.
func (p *residentIngressPending) finishServe(c *residentIngressServeCall, normal *bool, resultErr *error) {
	cause := recover()
	p.mu.Lock()
	c.returned, c.abnormal, c.err, c.cause = *normal, !*normal, *resultErr, cause
	if !*normal && p.firstError == nil {
		p.firstError = &residentServerAbnormal{cause: cause}
	}
	if *normal && *resultErr != nil && !errors.Is(*resultErr, http.ErrServerClosed) && p.firstError == nil {
		p.firstError = *resultErr
	}
	p.mu.Unlock()
	if *normal {
		return
	}
	for {
		p.mu.Lock()
		c.parkAttempted = true
		c.parkReturned = false
		p.mu.Unlock()
		time.Sleep(24 * time.Hour)
		p.mu.Lock()
		c.parkReturned = true
		p.mu.Unlock()
	}
}
