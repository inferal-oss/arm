package receiver

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/inferal-oss/arm/third_party/rsyncgo"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncchecksum"
	"github.com/inferal-oss/arm/third_party/rsyncgo/internal/rsyncopts"
)

const (
	maxFileListEntries = 1 << 18
	maxSymlinkTarget   = 4096
)

// rsync/flist.c:flist_sort_and_clean
func sortFileList(fileList []*File) {
	sort.Slice(fileList, func(i, j int) bool {
		return fileList[i].Name < fileList[j].Name
	})
}

// rsync/receiver.c:delete_files
func findInFileList(fileList []*File, name string) bool {
	i := sort.Search(len(fileList), func(i int) bool {
		return fileList[i].Name >= name
	})
	return i < len(fileList) && fileList[i].Name == name
}

type File struct {
	Name       string
	Length     int64
	ModTime    time.Time
	Mode       int32
	Uid        int32
	Gid        int32
	LinkTarget string
	Rdev       int32
	Checksum   [rsyncchecksum.Size]byte
}

// FileMode converts from the Linux permission bits to Go’s permission bits.
func (f *File) FileMode() fs.FileMode {
	ret := fs.FileMode(f.Mode) & fs.ModePerm

	mode := f.Mode & rsync.S_IFMT
	switch mode {
	case rsync.S_IFCHR:
		ret |= fs.ModeCharDevice
	case rsync.S_IFBLK:
		ret |= fs.ModeDevice
	case rsync.S_IFIFO:
		ret |= fs.ModeNamedPipe
	case rsync.S_IFSOCK:
		ret |= fs.ModeSocket
	case rsync.S_IFLNK:
		ret |= fs.ModeSymlink
	case rsync.S_IFDIR:
		ret |= fs.ModeDir
	}

	return ret
}

// rsync/flist.c:receive_file_entry
func (rt *Transfer) receiveFileEntry(flags uint16, last *File) (*File, error) {
	f := &File{}

	var l1 int
	if flags&rsync.XMIT_SAME_NAME != 0 {
		l, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		l1 = int(l)
		if l1 > len(last.Name) {
			return nil, fmt.Errorf("invalid inherited path prefix length %d", l1)
		}
	}

	var l2 int
	if flags&rsync.XMIT_LONG_NAME != 0 {
		l, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		l2 = int(l)
	} else {
		l, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		l2 = int(l)
	}
	if l2 < 0 {
		return nil, fmt.Errorf("invalid path suffix length %d", l2)
	}
	// linux/limits.h
	const PATH_MAX = 4096
	if l2 >= PATH_MAX-l1 {
		const lastname = ""
		return nil, fmt.Errorf("overflow: flags=0x%x l1=%d l2=%d lastname=%s",
			flags, l1, l2, lastname)
	}
	b := make([]byte, l1+l2)
	readb := b
	if l1 > 0 {
		copy(b, []byte(last.Name))
		readb = b[l1:]
	}
	if _, err := io.ReadFull(rt.Conn.Reader, readb); err != nil {
		return nil, err
	}
	// TODO: does rsync’s clean_fname() and sanitize_path() combination do
	// anything more than Go’s filepath.Clean()?
	rawName := string(b)
	if !utf8.ValidString(rawName) || strings.ContainsRune(rawName, 0) ||
		(rawName != "." && (!filepath.IsLocal(rawName) || path.Clean(rawName) != rawName || filepath.Clean(rawName) != rawName)) {
		return nil, fmt.Errorf("invalid wire path %q", rawName)
	}
	f.Name = rawName

	length, err := rt.Conn.ReadInt64()
	if err != nil {
		return nil, err
	}
	f.Length = length
	if length < 0 || length > rsync.MaxFileLength {
		return nil, fmt.Errorf("unsupported file length %d", length)
	}

	if flags&rsync.XMIT_SAME_TIME != 0 {
		f.ModTime = last.ModTime
	} else {
		modTime, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		f.ModTime = time.Unix(int64(modTime), 0)
	}

	if flags&rsync.XMIT_SAME_MODE != 0 {
		f.Mode = last.Mode
	} else {
		mode, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		f.Mode = mode
	}

	if rt.Opts.PreserveUid {
		if flags&rsync.XMIT_SAME_UID != 0 {
			f.Uid = last.Uid
		} else {
			uid, err := rt.Conn.ReadInt32()
			if err != nil {
				return nil, err
			}
			f.Uid = uid
		}
	}

	if rt.Opts.PreserveGid {
		if flags&rsync.XMIT_SAME_GID != 0 {
			f.Gid = last.Gid
		} else {
			gid, err := rt.Conn.ReadInt32()
			if err != nil {
				return nil, err
			}
			f.Gid = gid
		}
	}

	mode := f.Mode & rsync.S_IFMT
	isDev := mode == rsync.S_IFCHR || mode == rsync.S_IFBLK
	isSpecial := mode == rsync.S_IFIFO || mode == rsync.S_IFSOCK
	isLink := mode == rsync.S_IFLNK

	if rt.Opts.PreserveDevices && (isDev || isSpecial) {
		// TODO(protocol >= 28): rdev/major/minor handling
		if flags&rsync.XMIT_SAME_RDEV_pre28 != 0 {
			f.Rdev = last.Rdev
		} else {
			rdev, err := rt.Conn.ReadInt32()
			if err != nil {
				return nil, err
			}
			f.Rdev = rdev
		}
	}

	if rt.Opts.PreserveLinks && isLink {
		length, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		if length < 0 || length > maxSymlinkTarget {
			return nil, fmt.Errorf("invalid symlink target length %d", length)
		}
		b := make([]byte, length)
		if _, err := io.ReadFull(rt.Conn.Reader, b); err != nil {
			return nil, err
		}
		f.LinkTarget = string(b)
	}

	if rt.Opts.AlwaysChecksum {
		if _, err := io.ReadFull(rt.Conn.Reader, f.Checksum[:]); err != nil {
			return nil, err
		}
	}
	if mode != rsync.S_IFREG && mode != rsync.S_IFDIR && !(mode == rsync.S_IFLNK && rt.Opts.PreserveLinks) {
		if !(rt.Opts.PreserveDevices || rt.Opts.PreserveSpecials) {
			return nil, fmt.Errorf("unsupported wire file type for %q", f.Name)
		}
	}

	return f, nil
}

// rsync/flist.c:recv_file_list
func (rt *Transfer) ReceiveFileList() ([]*File, error) {
	if rt.Opts.Progress {
		fmt.Fprintln(rt.Env.Stdout, "receiving file list...")
		fmt.Fprint(rt.Env.Stdout, "0 files to consider")
	}
	lastFileEntry := new(File)
	var fileList []*File
	for {
		b, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			break
		}
		flags := uint16(b)
		// rt.Logger.Printf("flags: %x", flags)
		// TODO(protocol >= 28): extended flags

		f, err := rt.receiveFileEntry(flags, lastFileEntry)
		if err != nil {
			return nil, err
		}
		if len(fileList) >= maxFileListEntries {
			return nil, fmt.Errorf("file list exceeds %d entries", maxFileListEntries)
		}
		lastFileEntry = f
		// TODO: include depth in output?
		if rt.Opts.DebugGTE(rsyncopts.DEBUG_FLIST, 1) {
			rt.Logger.Printf("[Receiver] i=%d ? %s mode=%o len=%d uid=%d gid=%d flags=?",
				len(fileList),
				f.Name,
				f.Mode,
				f.Length,
				f.Uid,
				f.Gid)
		}
		fileList = append(fileList, f)
		if rt.Opts.Progress && len(fileList)%100 == 0 {
			fmt.Fprintf(rt.Env.Stdout, "\r%d files to consider", len(fileList))
		}
	}
	if rt.Opts.Progress {
		fmt.Fprintf(rt.Env.Stdout, "\r%d files to consider\n", len(fileList))
	}

	sortFileList(fileList)
	caseFolded := make(map[string]string, len(fileList))
	for i := 1; i < len(fileList); i++ {
		if fileList[i-1].Name == fileList[i].Name {
			return nil, fmt.Errorf("duplicate wire path %q", fileList[i].Name)
		}
	}
	for _, f := range fileList {
		folded := strings.ToLower(f.Name)
		if old, ok := caseFolded[folded]; ok && old != f.Name {
			return nil, fmt.Errorf("case-colliding wire paths %q and %q", old, f.Name)
		}
		caseFolded[folded] = f.Name
	}

	if rt.Opts.PreserveUid || rt.Opts.PreserveGid {
		// receive the uid/gid list
		users, groups, err := rt.RecvIdList()
		if err != nil {
			return nil, err
		}
		rt.Users = users
		rt.Groups = groups
	}

	// read the i/o error flag
	ioErrors, err := rt.Conn.ReadInt32()
	if err != nil {
		return nil, err
	}
	if rt.Opts.DebugGTE(rsyncopts.DEBUG_FLIST, 2) {
		rt.Logger.Printf("ioErrors: %v", ioErrors)
	}
	rt.IOErrors = ioErrors

	return fileList, nil
}
