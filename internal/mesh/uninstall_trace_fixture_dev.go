//go:build arm_devtrace

package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/buildinfo"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// DevUninstallPublicationInput creates only a fresh selected test-root fixture.
// The file identities/hash/copy are real; the original provisioning/runtime
// report is an explicitly supplied test boundary, not claimed as an executed
// publication/SSH helper. Actual Stop/lock comes separately from real Serve.
// No wire route or environment variable calls this function.
func DevUninstallPublicationInput(root string, stop nodestop.Prepared) (meshssh.Transfer, error) {
	var q meshssh.Transfer
	if _, ok := uninstallDevObservers.Load(root); !ok {
		return q, fmt.Errorf("selected fresh fixture root required")
	}
	image := []byte("private uninstall model fixture; never executed\n")
	h := sha256.Sum256(image)
	q = meshssh.Transfer{Root: root, Key: strings.Repeat("1", 64), SHA256: hex.EncodeToString(h[:]), Size: int64(len(image)),
		ExpectedUID: strconv.Itoa(os.Geteuid()), OS: runtime.GOOS, Arch: runtime.GOARCH, Version: buildinfo.Version,
		Protocol: LifecycleProtocol, Reason: "explicit original-publication test input"}
	fd, unlock, e := provisionLock(q)
	if e != nil {
		return q, e
	}
	held := true
	defer func() {
		if held {
			unlock()
		}
	}()
	path := filepath.Dir(UninstallInstalledPath(q))
	if e = os.Mkdir(path, 0700); e != nil {
		return q, e
	}
	if e = os.WriteFile(filepath.Join(path, "arm"), image, 0700); e != nil {
		return q, e
	}
	artifact, e := directory(path)
	if e != nil {
		return q, e
	}
	proof, e := captureInstallOwnership(fd, artifact, q)
	closeErr := artifact.Close()
	if e != nil {
		return q, e
	}
	if closeErr != nil {
		return q, closeErr
	}
	// The current registration is real and bound to the new file/native proof.
	// It must run outside provisionLock, exactly as ordinary registration does.
	unlock()
	held = false
	if e = registerManagedExecutable(UninstallInstalledPath(q), stop.Plan.Target.Home); e != nil {
		return q, e
	}
	_, receiptUnlock, e := provisionExistingLock(q)
	if e != nil {
		return q, e
	}
	defer receiptUnlock()
	startup := DefaultStartup()
	original := RuntimeProof{Host: stop.Plan.Target.Host, UID: q.ExpectedUID, SHA256: q.SHA256, Size: q.Size,
		Version: q.Version, Protocol: q.Protocol, Method: "startup-open-file-snapshot", Captured: time.Now().UTC()}
	r := ProvisionReceipt{Schema: 1, Request: q, Actor: local.CurrentIdentity(), Phase: "started_verified", Binary: UninstallInstalledPath(q),
		Version: buildinfo.Version, Protocol: LifecycleProtocol, StartHome: stop.Plan.Target.Home, StartedHost: stop.Plan.Target.Host,
		Ownership: proof, Startup: &startup, Runtime: &original}
	// The same real receipt writer; the runtime premise above stays explicit.
	if e = saveProvision(q, &r); e != nil {
		return q, e
	}
	return q, nil
}

func DevUninstallDriver(action string, p UninstallPlan, resultErr error) {
	traceUninstall(action, p, nil, resultErr)
}
func DevUninstallSourceObserved(action string, p UninstallPlan, home string) error {
	op, present, e := ReadCoordination(home, p.Key)
	if e != nil {
		return e
	}
	if !present {
		return fmt.Errorf("source receipt absent")
	}
	traceUninstallSource(action, p, home, op, nil)
	return nil
}
