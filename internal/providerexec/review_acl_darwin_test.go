package providerexec

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReviewRejectACLWritableSelectedNamespace(t *testing.T) {
	for _, surface := range []string{"directory", "leaf"} {
		t.Run(surface, func(t *testing.T) {
			dir, err := os.MkdirTemp("/private/tmp", "arm-provider-acl-review-")
			if err != nil { t.Fatal(err) }
			initialDir, err := os.Lstat(dir)
			if err != nil { t.Fatal(err) }
			path := filepath.Join(dir, "provider")
			var initialFile os.FileInfo
			t.Cleanup(func() {
				cur, e := os.Lstat(dir)
				if e != nil || !os.SameFile(initialDir, cur) { t.Error("changed fixture directory retained", dir); return }
				if out, e := exec.Command("/bin/chmod", "-N", dir).CombinedOutput(); e != nil { t.Error(e, string(out)); return }
				if e = os.Chmod(dir,0700); e != nil { t.Error(e); return }
				if cur,e = os.Lstat(path); e == nil {
					if initialFile == nil || !os.SameFile(initialFile,cur) { t.Error("changed fixture file retained", path); return }
					if out,e := exec.Command("/bin/chmod", "-N", path).CombinedOutput(); e != nil { t.Error(e, string(out));return }
					if e = os.Remove(path);e != nil {t.Error(e);return}
				} else if !os.IsNotExist(e) {t.Error(e);return}
				if e = os.Remove(dir);e != nil {t.Error("nonempty fixture directory retained",e)}
			})
			executable, err := os.Executable()
			if err != nil {t.Fatal(err)}
			src, err := os.Open(executable)
			if err != nil {t.Fatal(err)}
			defer src.Close()
			dst, err := os.OpenFile(path,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0700)
			if err != nil {t.Fatal(err)}
			_,err=io.Copy(dst,src)
			if err == nil {err=dst.Sync()}
			dst.Close()
			if err != nil {t.Fatal(err)}
			initialFile,err=os.Lstat(path)
			if err != nil {t.Fatal(err)}
			if _,err=PinImage(path,false);err != nil {t.Fatal("safe control rejected",err)}
			target, ace := dir, "everyone allow list,search,add_file,delete_child,readattr,readsecurity"
			if surface=="leaf" {target,ace=path,"everyone allow write,append,delete,readattr,readsecurity"}
			if out,e := exec.Command("/bin/chmod", "+a", ace,target).CombinedOutput();e != nil {t.Fatal("actual ACL setup failed",e,string(out))}
			info,err:=os.Lstat(target)
			if err != nil || info.Mode().Perm()!=0700 {t.Fatal("ACL fixture must retain protected POSIX mode",info,err)}
			out,err:=exec.Command("/bin/ls","-lde",target).CombinedOutput()
			if err != nil {t.Fatal(err)}
			t.Logf("actual native ACL fixture: %s",out)
			if selected,e:=PinImage(path,false);e == nil {t.Fatalf("ACL-writable %s accepted with mode0700: digest=%s",surface,selected.SHA256())}
		})
	}
}
