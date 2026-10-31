package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"path/filepath"
	"sync"

	"getarm.org/internal/residentrefs"
)

var errResidentInheritedImage = errors.New("actual original inherited image binding required")
var errResidentInheritedBorrowedClose = errors.New("inherited runtime is a nonclosing borrower; retain actual original image owner")

type InheritedImagePending struct{ state *inheritedImageState }
type inheritedImageWaiter struct{ identity byte }
type inheritedImageState struct {
	waiters               map[*inheritedImageWaiter]struct{}
	mu                    sync.Mutex
	self                  *InheritedImagePending
	custody               *residentrefs.ImageOriginal
	view                  *residentrefs.ImageView
	runtime               *ResidentRuntimePending
	rootContext           context.Context
	active                *inheritedImageOperation
	verifyTried, verified bool
	firstError            error
	expected              ResidentImageInventory
	identities            residentrefs.ImageIdentities
	buffer                []byte
	copied                int64
	hasher                hash.Hash
	image                 *RuntimeExecutable
}
type inheritedImageOperation struct {
	done        chan struct{}
	kind, phase string
	abnormal    bool
	cause       any
}

// Pure construction before same-entry delegation/native effects. Actual root
// retains this source/kernel-image pending and delegated image owner for life.
func NewInheritedImagePending(rootContext context.Context, custody *residentrefs.ImageOriginal) (*InheritedImagePending, error) {
	if rootContext == nil || custody == nil {
		return nil, errResidentInheritedImage
	}
	p := &InheritedImagePending{}
	s := &inheritedImageState{self: p, custody: custody, rootContext: rootContext, runtime: NewResidentRuntimePending()}
	p.state = s
	view, err := custody.ClaimImageOriginalView()
	if err != nil {
		return nil, err
	}
	s.view = view
	return p, nil
}

// Request cancellation affects only queue admission; native effects retain the
// independent original root lifetime. No caller context replaces that lifetime.
func (p *InheritedImagePending) originalWaitContext() context.Context {
	if p == nil || p.state == nil {
		return nil
	}
	return p.state.rootContext
}
func (p *InheritedImagePending) begin(waitContext context.Context, kind string) (*inheritedImageOperation, error) {
	if p == nil || p.state == nil || p.state.self != p || waitContext == nil {
		return nil, errResidentInheritedImage
	}
	s := p.state
	waiter := &inheritedImageWaiter{}
	queued := false
	defer func() {
		if queued {
			s.mu.Lock()
			delete(s.waiters, waiter)
			s.mu.Unlock()
		}
	}()
	for {
		s.mu.Lock()
		if err := waitContext.Err(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if s.firstError != nil {
			s.mu.Unlock()
			return nil, s.firstError
		}
		if active := s.active; active != nil {
			if kind != "proof" {
				s.mu.Unlock()
				return nil, ErrResidentRuntimePending
			}
			if !queued {
				if len(s.waiters) >= 64 {
					s.mu.Unlock()
					return nil, residentrefs.ErrBusy
				}
				if s.waiters == nil {
					s.waiters = make(map[*inheritedImageWaiter]struct{})
				}
				s.waiters[waiter] = struct{}{}
				queued = true
			}
			done := active.done
			s.mu.Unlock()
			select {
			case <-waitContext.Done():
				return nil, waitContext.Err()
			case <-s.rootContext.Done():
				return nil, s.rootContext.Err()
			case <-done:
				continue
			}
		}
		if queued {
			delete(s.waiters, waiter)
			queued = false
		}
		if kind == "verify" {
			if s.verifyTried {
				s.mu.Unlock()
				return nil, errResidentInheritedImage
			}
			s.verifyTried = true
		} else if kind != "proof" || !s.verified {
			s.mu.Unlock()
			return nil, errResidentInheritedImage
		}
		op := &inheritedImageOperation{kind: kind, phase: "registered_before_effect", done: make(chan struct{})}
		s.active = op
		s.mu.Unlock()
		return op, nil
	}
}

func (s *inheritedImageState) phase(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		panic("inherited image operation missing")
	}
	s.active.phase = value
}
func (s *inheritedImageState) finish(op *inheritedImageOperation, normal *bool, resultErr *error) {
	cause := recover()
	s.mu.Lock()
	if s.active != op {
		s.mu.Unlock()
		panic("inherited image operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if s.firstError == nil {
			s.firstError = &residentRuntimeAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && !errors.Is(*resultErr, residentrefs.ErrBusy) && s.firstError == nil {
			s.firstError = *resultErr
		}
		if *resultErr == nil && op.kind == "verify" {
			s.verified = true
		}
		s.active = nil
		close(op.done)
	}
	s.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (p *InheritedImagePending) VerifyOriginal(ctx context.Context, expected ResidentImageInventory) (resultErr error) {
	op, err := p.begin(ctx, "verify")
	if err != nil {
		return err
	}
	s := p.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	s.expected = expected
	resultErr = s.verifyBody(ctx)
	normal = true
	return resultErr
}
func (s *inheritedImageState) verifyBody(ctx context.Context) error {
	if ctx == nil {
		return errResidentInheritedImage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.expected.Path != filepath.Join(s.expected.Home, "resident-images", "pin-"+s.expected.DebitID, "arm") || s.expected.Size <= 0 || s.expected.Size > MaxArtifactBytes || len(s.expected.SHA256) != 64 {
		return errResidentInheritedImage
	}
	if err := s.custody.MaterializeOriginal(ctx); err != nil {
		return err
	}
	identities, err := s.custody.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	s.identities = identities
	if identities.Image != inheritedID(s.expected.ImageID) || identities.Images != inheritedID(s.expected.ImagesID) || identities.Directory != inheritedID(s.expected.DirectoryID) {
		return errResidentInheritedImage
	}
	before, err := s.view.StatOriginal(ctx)
	if err != nil {
		return err
	}
	if before.Size() != s.expected.Size {
		return errResidentInheritedImage
	}
	s.buffer = make([]byte, 64<<10)
	s.hasher = sha256.New()
	for s.copied < s.expected.Size {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.phase("original_inherited_readonly_image_hash")
		want := min(int64(len(s.buffer)), s.expected.Size-s.copied)
		n, readErr := s.view.ReadAtOriginal(ctx, s.buffer[:int(want)], s.copied)
		if n > 0 {
			if _, err = s.hasher.Write(s.buffer[:n]); err != nil {
				return err
			}
			s.copied += int64(n)
		}
		if readErr != nil && !(errors.Is(readErr, io.EOF) && s.copied == s.expected.Size) {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	if hex.EncodeToString(s.hasher.Sum(nil)) != s.expected.SHA256 {
		return errResidentInheritedImage
	}
	s.phase("actual_child_current_runtime_capture_pending")
	if err = s.runtime.CaptureOriginal(ctx); err != nil {
		return err
	}
	proof, err := s.runtime.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	kernel, err := s.runtime.IdentityOriginal(ctx)
	if err != nil {
		return err
	}
	if kernel != identities.Image || proof.SHA256 != s.expected.SHA256 || proof.Size != s.expected.Size || s.runtime.path != s.expected.Path {
		return errResidentInheritedImage
	}
	after, err := s.view.StatOriginal(ctx)
	if err != nil {
		return err
	}
	if !sameArtifactStat(before, after) {
		return errResidentInheritedImage
	}
	// The actual captured runtime remains under its original pending owner; this
	// object is only a nonclosing proof/path borrower for native issuer/helpers.
	s.image = s.runtime.image
	s.image.inherited = s.self
	return ctx.Err()
}
func inheritedID(id ResidentImageInventoryID) residentrefs.Identity {
	return residentrefs.Identity{Device: id.Device, Inode: id.Inode, UID: id.UID, GID: id.GID, Mode: id.Mode}
}
func (p *InheritedImagePending) proofOriginal(image *RuntimeExecutable) (proof RuntimeProof, resultErr error) {
	if p == nil || p.state == nil || p.state.self != p || image == nil || p.state.image != image {
		return proof, errResidentInheritedImage
	}
	op, err := p.begin(p.originalWaitContext(), "proof")
	if err != nil {
		return proof, err
	}
	s := p.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	identities, err := s.custody.ValidateOriginal(s.rootContext)
	resultErr = err
	if resultErr == nil && identities != s.identities {
		resultErr = errResidentInheritedImage
	}
	if resultErr == nil {
		proof, resultErr = s.runtime.ValidateOriginal(s.rootContext)
	}
	if resultErr == nil && (proof.SHA256 != s.expected.SHA256 || proof.Size != s.expected.Size) {
		resultErr = errResidentInheritedImage
	}
	normal = true
	return proof, resultErr
}

// The returned existing native/helper type cannot Close its actual originals:
// RuntimeExecutable.Close explicitly refuses this inherited nonclosing borrower.
func (p *InheritedImagePending) RuntimeForOriginalRoot() (*RuntimeExecutable, error) {
	if p == nil || p.state == nil || p.state.self != p {
		return nil, errResidentInheritedImage
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.verified || s.active != nil || s.firstError != nil || s.image == nil || s.image.inherited != p {
		return nil, errResidentInheritedImage
	}
	return s.image, nil
}
