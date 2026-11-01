package residentrefs

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"getarm.org/internal/local"
	"golang.org/x/sys/unix"
)

const ResidentControlFilename = "resident-control.json"
const MaxResidentControlBytes = 16 << 10
const maxControlPublicationWrites = 32

var originalControlPublication struct {
	mu      sync.Mutex
	service *ServiceOriginal
	pending *ResidentControlPublicationPending
}

type ResidentControlPublicationPending struct {
	mu         sync.Mutex
	self       *ResidentControlPublicationPending
	service    *ServiceOriginal
	home       *originalEntry
	call       *controlPublicationCall
	firstError error
}
type controlPublicationResult struct {
	attempted, returned bool
	err                 error
}
type controlPublicationWrite struct {
	controlPublicationResult
	requested, written int
}
type controlPublicationCall struct {
	owner                                                                       *ResidentControlPublicationPending
	ctx                                                                         context.Context
	payload, canonical                                                          []byte
	endpoint                                                                    local.Endpoint
	temporary                                                                   string
	claim                                                                       controlPublicationResult
	borrower                                                                    *originalOperation
	homeStat, destinationStat, open, materialize, fileStat, sync, close, rename controlPublicationResult
	homeDescriptorFlags, homeAccessFlags                                        controlPublicationResult
	homeFDFlags, homeFLFlags                                                    int
	tempFD                                                                      int
	tempFile                                                                    *os.File
	writes                                                                      [maxControlPublicationWrites]controlPublicationWrite
	writeCount, written                                                         int
	returned, abnormal, published                                               bool
	err                                                                         error
	cause                                                                       any
}

// Construction issues only this one fixed-name publisher on the ACTUAL original
// service recipient. It does not prove handoff or Ready: the retained Root must
// gate PublishOriginal with its own completed original operations.
func NewOriginalResidentControlPublication(source *ServiceOriginal) (*ResidentControlPublicationPending, error) {
	if source == nil || source.self != source || source.state == nil || source.state.census == nil {
		return nil, ErrOriginal
	}
	r := source.state
	s := r.census
	s.mu.Lock()
	valid := s.self != nil && s.self.state == s && s.self.ownsProcessClaim() && s.service == source && r.original == source && r.role == "service" && s.delegated && s.rootCloseRetired && s.sealed && s.firstError == nil && r.firstError == nil && r.materialized && r.count == 3
	var home *originalEntry
	if valid {
		home = r.entries[0]
		valid = home != nil && home.self == home && home.census == s && home.recipient == r && home.reservedRecipient == r && home.file != nil && !home.closeTried && home.firstError == nil
	}
	s.mu.Unlock()
	if !valid {
		return nil, ErrOriginal
	}
	originalControlPublication.mu.Lock()
	defer originalControlPublication.mu.Unlock()
	if originalControlPublication.service != nil || originalControlPublication.pending != nil {
		return nil, ErrOriginal
	}
	originalControlPublication.service = source // retained before allocation
	p := &ResidentControlPublicationPending{service: source, home: home}
	p.self = p
	originalControlPublication.pending = p
	return p, nil
}

// Pure DATA validation only. An endpoint, token, PID or generation is not an
// original publisher, native identity, successful publication or public Ready.
func validateResidentControlData(raw []byte) (local.Endpoint, []byte, error) {
	var value local.Endpoint
	if len(raw) == 0 || len(raw) > MaxResidentControlBytes {
		return value, nil, ErrOriginal
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return value, nil, ErrOriginal
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.PID <= 0 {
		return value, canonical, ErrOriginal
	}
	for _, v := range []struct {
		text  string
		bytes int
	}{{value.Generation, 16}, {value.Token, 32}} {
		decoded, e := hex.DecodeString(v.text)
		if e != nil || len(decoded) != v.bytes || strings.ToLower(v.text) != v.text {
			return value, canonical, ErrOriginal
		}
	}
	if len(value.URL) > 512 {
		return value, canonical, ErrOriginal
	}
	u, err := url.Parse(value.URL)
	if err != nil || u.Scheme != "ws" || u.User != nil || u.Path != "/v1/ws" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return value, canonical, ErrOriginal
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return value, canonical, ErrOriginal
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return value, canonical, ErrOriginal
	}
	return value, canonical, nil
}

func (p *ResidentControlPublicationPending) begin(ctx context.Context, raw []byte) (*controlPublicationCall, error) {
	if p == nil || p.self != p || p.service == nil || p.home == nil {
		return nil, ErrOriginal
	}
	originalControlPublication.mu.Lock()
	valid := originalControlPublication.pending == p && originalControlPublication.service == p.service
	originalControlPublication.mu.Unlock()
	if !valid {
		return nil, ErrOriginal
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.call != nil || p.firstError != nil {
		return nil, ErrOriginal
	}
	c := &controlPublicationCall{owner: p, ctx: ctx, tempFD: -1}
	p.call = c // original operation before payload allocation/validation or native work
	if len(raw) == 0 || len(raw) > MaxResidentControlBytes {
		c.err = ErrOriginal
		return c, ErrOriginal
	}
	c.payload = append([]byte(nil), raw...)
	return c, nil
}

func (p *ResidentControlPublicationPending) finish(c *controlPublicationCall, normal *bool, resultErr *error) {
	cause := recover()
	p.mu.Lock()
	if p.call != c || c.owner != p {
		p.mu.Unlock()
		panic("original control publication identity lost")
	}
	c.returned, c.abnormal, c.err, c.cause = *normal, !*normal, *resultErr, cause
	if !*normal && p.firstError == nil {
		p.firstError = &abnormalOriginal{cause: cause}
	}
	if *resultErr != nil && p.firstError == nil {
		p.firstError = *resultErr
	}
	p.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (p *ResidentControlPublicationPending) PublishOriginal(ctx context.Context, raw []byte) (resultErr error) {
	c, err := p.begin(ctx, raw)
	if c == nil {
		return err
	}
	normal := false
	defer p.finish(c, &normal, &resultErr)
	if err != nil {
		normal = true
		return err
	}
	if ctx == nil {
		normal = true
		return ErrOriginal
	}
	if err = ctx.Err(); err != nil {
		normal = true
		return err
	}
	c.endpoint, c.canonical, resultErr = validateResidentControlData(c.payload)
	if resultErr != nil {
		normal = true
		return resultErr
	}
	c.temporary = ".resident-control-" + c.endpoint.Generation + ".tmp"
	c.claim.attempted = true
	c.borrower, c.claim.err = p.service.state.beginView(ctx, "publish_resident_control", p.home)
	c.claim.returned = true
	if c.claim.err != nil {
		normal = true
		return c.claim.err
	}
	defer p.service.state.census.finish(c.borrower, &normal, &resultErr)
	resultErr = p.publishBody(c)
	normal = true
	return resultErr
}

func (p *ResidentControlPublicationPending) before(c *controlPublicationCall, phase string) error {
	return p.service.state.census.before(c.borrower, phase)
}

func (p *ResidentControlPublicationPending) publishBody(c *controlPublicationCall) error {
	if err := p.before(c, "held_original_control_home_stat"); err != nil {
		return err
	}
	c.homeStat.attempted = true
	var st unix.Stat_t
	c.homeStat.err = unix.Fstat(p.home.number, &st)
	c.homeStat.returned = true
	if c.homeStat.err != nil {
		return c.homeStat.err
	}
	id := Identity{uint64(st.Dev), uint64(st.Ino), st.Uid, st.Gid, uint32(st.Mode)}
	if id != p.home.identity || uint32(st.Mode) != uint32(unix.S_IFDIR)|0700 || st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 {
		return ErrOriginal
	}
	if err := p.before(c, "held_control_home_descriptor_flags"); err != nil {
		return err
	}
	c.homeDescriptorFlags.attempted = true
	c.homeFDFlags, c.homeDescriptorFlags.err = unix.FcntlInt(uintptr(p.home.number), unix.F_GETFD, 0)
	c.homeDescriptorFlags.returned = true
	if c.homeDescriptorFlags.err != nil {
		return c.homeDescriptorFlags.err
	}
	if c.homeFDFlags&unix.FD_CLOEXEC == 0 {
		return ErrOriginal
	}
	if err := p.before(c, "held_control_home_access_flags"); err != nil {
		return err
	}
	c.homeAccessFlags.attempted = true
	c.homeFLFlags, c.homeAccessFlags.err = unix.FcntlInt(uintptr(p.home.number), unix.F_GETFL, 0)
	c.homeAccessFlags.returned = true
	if c.homeAccessFlags.err != nil {
		return c.homeAccessFlags.err
	}
	if c.homeFLFlags&unix.O_ACCMODE != unix.O_RDONLY {
		return ErrOriginal
	}
	if err := p.before(c, "fixed_control_destination_must_be_absent"); err != nil {
		return err
	}
	c.destinationStat.attempted = true
	c.destinationStat.err = unix.Fstatat(p.home.number, ResidentControlFilename, &st, unix.AT_SYMLINK_NOFOLLOW)
	c.destinationStat.returned = true
	if !errors.Is(c.destinationStat.err, unix.ENOENT) {
		return ErrOriginal
	}
	if err := p.before(c, "fixed_control_temporary_open"); err != nil {
		return err
	}
	c.open.attempted = true
	c.tempFD, c.open.err = unix.Openat(p.home.number, c.temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	c.open.returned = true // raw descriptor retained before any wrapper/stat
	if c.open.err != nil {
		return c.open.err
	}
	if c.tempFD < 0 {
		return ErrOriginal
	}
	if err := p.before(c, "control_temporary_materialization"); err != nil {
		return err
	}
	c.materialize.attempted = true
	c.tempFile = os.NewFile(uintptr(c.tempFD), "original resident control publication")
	c.materialize.returned = true
	if c.tempFile == nil {
		return ErrOriginal
	}
	if err := p.before(c, "control_temporary_stat"); err != nil {
		return err
	}
	c.fileStat.attempted = true
	c.fileStat.err = unix.Fstat(c.tempFD, &st)
	c.fileStat.returned = true
	if c.fileStat.err != nil {
		return c.fileStat.err
	}
	if uint32(st.Mode) != uint32(unix.S_IFREG)|0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size != 0 {
		return ErrOriginal
	}
	for c.written < len(c.payload) {
		if c.writeCount >= len(c.writes) {
			return io.ErrShortWrite
		}
		if err := p.before(c, "original_control_payload_write"); err != nil {
			return err
		}
		w := &c.writes[c.writeCount]
		c.writeCount++
		w.requested = len(c.payload) - c.written
		w.attempted = true
		w.written, w.err = c.tempFile.Write(c.payload[c.written:])
		w.returned = true
		if w.written < 0 || w.written > w.requested {
			return ErrOriginal
		}
		c.written += w.written
		if w.err != nil {
			return w.err
		}
		if w.written == 0 {
			return io.ErrShortWrite
		}
	}
	if err := p.before(c, "original_control_payload_sync"); err != nil {
		return err
	}
	c.sync.attempted = true
	c.sync.err = c.tempFile.Sync()
	c.sync.returned = true
	if c.sync.err != nil {
		return c.sync.err
	}
	if err := p.before(c, "original_control_temporary_close"); err != nil {
		return err
	}
	c.close.attempted = true
	c.close.err = c.tempFile.Close()
	c.close.returned = true
	if c.close.err != nil {
		return c.close.err
	}
	if err := p.before(c, "original_control_exclusive_publish"); err != nil {
		return err
	}
	c.rename.attempted = true
	c.rename.err = renameResidentControlExclusive(p.home.number, c.temporary, ResidentControlFilename)
	c.rename.returned = true
	if c.rename.err != nil {
		return c.rename.err
	}
	c.published = true // observed publication, NOT directory crash durability or Ready
	return c.ctx.Err()
}
