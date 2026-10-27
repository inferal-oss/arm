package syncfs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const workspaceMarkerName = ".arm-sync-owner.json"
const maxWorkspaceMarkerBytes = 4096

// workspaceMarker is written only after the private directories are created
// and synced. An interrupted creation without this marker remains unknown.
// The marker is evidence for inspection, never cleanup or release authority.
type workspaceMarker struct {
	Version        int         `json:"version"`
	Reservation    Reservation `json:"reservation"`
	SelectedRootID string      `json:"selected_root_id"`
	ParentRootID   string      `json:"parent_root_id"`
	BaseRootID     string      `json:"base_root_id"`
	StageRootID    string      `json:"stage_root_id"`
	QuarantineID   string      `json:"quarantine_root_id"`
}

func writeWorkspaceMarker(selected *Root, w *Workspace) error {
	selectedID, err := selected.NativeID()
	if err != nil {
		return err
	}
	parentID, err := w.Parent.NativeID()
	if err != nil {
		return err
	}
	baseID, err := w.Base.NativeID()
	if err != nil {
		return err
	}
	stageID, err := w.Stage.NativeID()
	if err != nil {
		return err
	}
	quarantineID, err := w.Quarantine.NativeID()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(workspaceMarker{Version: 1, Reservation: w.Reservation, SelectedRootID: selectedID, ParentRootID: parentID, BaseRootID: baseID, StageRootID: stageID, QuarantineID: quarantineID})
	if err != nil || len(raw) > maxWorkspaceMarkerBytes {
		return ErrChanged
	}
	fd, err := unix.Openat(w.Base.FD(), workspaceMarkerName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), workspaceMarkerName)
	for len(raw) != 0 {
		n, e := f.Write(raw)
		if e != nil {
			_ = f.Close()
			return e
		}
		if n == 0 {
			_ = f.Close()
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := unix.Fsync(w.Base.FD()); err != nil {
		return err
	}
	if err := selected.Check(); err != nil {
		return err
	}
	if err := w.Stage.Check(); err != nil {
		return err
	}
	if err := w.Quarantine.Check(); err != nil {
		return err
	}
	return w.Base.Check()
}

func workspaceName(path, parent string) bool {
	if filepath.Clean(path) != path || filepath.Dir(path) != parent {
		return false
	}
	name := filepath.Base(path)
	if !strings.HasPrefix(name, ".arm-sync-") || len(name) != len(".arm-sync-")+24 {
		return false
	}
	nonce := strings.TrimPrefix(name, ".arm-sync-")
	_, err := hex.DecodeString(nonce)
	return err == nil && strings.ToLower(nonce) == nonce
}

func readWorkspaceMarker(base *Root) (workspaceMarker, error) {
	var marker workspaceMarker
	if err := base.Check(); err != nil {
		return marker, err
	}
	// A same-account replacement can be a FIFO. Refuse nonregular markers
	// without blocking in open before the descriptor type/size checks below.
	fd, err := unix.Openat(base.FD(), workspaceMarkerName, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return marker, err
	}
	f := os.NewFile(uintptr(fd), workspaceMarkerName)
	defer f.Close()
	st, err := statFD(fd)
	if err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size <= 0 || st.Size > maxWorkspaceMarkerBytes {
		return marker, ErrChanged
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxWorkspaceMarkerBytes+1))
	if err != nil || len(raw) != int(st.Size) {
		return marker, ErrChanged
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return marker, ErrChanged
	}
	canonical, err := json.Marshal(marker)
	if err != nil || !bytes.Equal(raw, canonical) {
		return marker, ErrChanged
	}
	if err := base.Check(); err != nil {
		return marker, err
	}
	return marker, nil
}

// InspectWorkspace reopens a named private workspace against the exact
// selected root, parent, and original reservation. It returns held handles
// for inspection only. A matching marker never authorizes deletion or proves
// physical reclamation; the caller must keep the charged lease unresolved.
func InspectWorkspace(selected, parent *Root, path string, want Reservation) (*Workspace, error) {
	if selected == nil || parent == nil || want.validate() != nil || !workspaceName(path, parent.Path()) {
		return nil, ErrChanged
	}
	if err := selected.Check(); err != nil {
		return nil, err
	}
	if err := parent.Check(); err != nil {
		return nil, err
	}
	base, err := OpenRoot(path)
	if err != nil {
		return nil, err
	}
	w := &Workspace{Reservation: want, Parent: parent, Base: base, path: path}
	closeOnError := func(err error) (*Workspace, error) { _ = w.Close(); return nil, err }
	marker, err := readWorkspaceMarker(base)
	if err != nil {
		return closeOnError(err)
	}
	selectedID, err := selected.NativeID()
	if err != nil {
		return closeOnError(err)
	}
	parentID, err := parent.NativeID()
	if err != nil {
		return closeOnError(err)
	}
	baseID, err := base.NativeID()
	if err != nil {
		return closeOnError(err)
	}
	if marker.Version != 1 || marker.Reservation != want || marker.SelectedRootID != selectedID || marker.ParentRootID != parentID || marker.BaseRootID != baseID {
		return closeOnError(ErrChanged)
	}
	if w.Stage, err = OpenRoot(filepath.Join(path, "stage")); err != nil {
		return closeOnError(err)
	}
	if w.Quarantine, err = OpenRoot(filepath.Join(path, "quarantine")); err != nil {
		return closeOnError(err)
	}
	stageID, err := w.Stage.NativeID()
	if err != nil {
		return closeOnError(err)
	}
	quarantineID, err := w.Quarantine.NativeID()
	if err != nil {
		return closeOnError(err)
	}
	if marker.StageRootID != stageID || marker.QuarantineID != quarantineID {
		return closeOnError(ErrChanged)
	}
	parentStat, err := statFD(parent.FD())
	if err != nil {
		return closeOnError(err)
	}
	selectedStat, err := statFD(selected.FD())
	if err != nil || parentStat.Dev != selectedStat.Dev || parentStat.Uid != uint32(os.Geteuid()) || parentStat.Mode&0077 != 0 {
		return closeOnError(ErrChanged)
	}
	for _, root := range []*Root{base, w.Stage, w.Quarantine} {
		st, e := statFD(root.FD())
		if e != nil || st.Dev != parentStat.Dev || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0777 != 0700 || st.Uid != uint32(os.Geteuid()) {
			return closeOnError(ErrChanged)
		}
		if e := root.Check(); e != nil {
			return closeOnError(e)
		}
	}
	if err := selected.Check(); err != nil {
		return closeOnError(err)
	}
	if err := parent.Check(); err != nil {
		return closeOnError(err)
	}
	return w, nil
}
