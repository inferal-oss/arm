package manager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
	"golang.org/x/sys/unix"
)

func gradleInstalledExecutable(command string) (string, error) {
	path := command
	var err error
	if !filepath.IsAbs(path) {
		path, err = exec.LookPath(path)
	}
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil {
		return "", model.Err("profile_conflict", "installed Gradle executable is unavailable")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", model.Err("profile_conflict", "Gradle executable must be an installed regular executable")
	}
	return path, nil
}

func gradleProjectRoot(path string) (string, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(root) {
		return "", model.Err("profile_conflict", "Gradle build needs an existing absolute project root")
	}
	settings := 0
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		info, err := os.Lstat(filepath.Join(root, name))
		if err == nil {
			if !info.Mode().IsRegular() {
				return "", model.Err("profile_conflict", "Gradle settings must be a regular file in the explicit project root")
			}
			settings++
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if settings != 1 {
		return "", model.Err("profile_conflict", "Gradle build requires exactly one settings.gradle or settings.gradle.kts in its explicit cwd; ancestor/composite root discovery is unsupported")
	}
	if _, err := os.Lstat(filepath.Join(root, "gradle", "gradle-daemon-jvm.properties")); err == nil || !os.IsNotExist(err) {
		return "", model.Err("profile_conflict", "Gradle daemon JVM criteria can select another JDK; this profile requires the owned service's pinned JDK")
	}
	if _, err := os.Lstat(filepath.Join(root, "buildSrc")); err == nil || !os.IsNotExist(err) {
		return "", model.Err("profile_conflict", "Gradle buildSrc introduces another build/cache scope; this initial profile supports explicit projects without buildSrc")
	}
	return root, nil
}

func (m *Manager) gradleParent(r model.Request, consumer string) (model.Lease, error) {
	parent, err := m.get(r.ServiceLease)
	if err != nil {
		return parent, err
	}
	j := m.jobs[parent.ID]
	if parent.Request.Profile != "gradle-service" || parent.Owner != m.owner || parent.Host != m.host || parent.State != "active" || parent.Workload == nil || parent.Workload.State != "ready" || j == nil || j.identity == nil || parent.Expires == nil || time.Now().Add(r.TTL).After(*parent.Expires) {
		return parent, model.Err("service_unavailable", "Gradle service must be verified ready, currently ARM-supervised on this host/user, and outlast the consumer")
	}
	current, err := usage.Capture(j.identity.PID)
	if err != nil || current != *j.identity || parent.Workload.ServiceProcess == nil || current != *parent.Workload.ServiceProcess {
		return parent, model.Err("service_unavailable", "Gradle service native identity changed or is unavailable")
	}
	private, err := m.gradleRecord(parent.ID)
	if err != nil {
		return parent, err
	}
	if err := m.checkGradleDirectories(private); err != nil {
		return parent, err
	}
	leases, err := m.list()
	if err != nil {
		return parent, err
	}
	for _, l := range leases {
		if l.ID != consumer && l.Request.ServiceLease == parent.ID && (consuming(l.State) || l.State == "queued") {
			return parent, model.Err("service_busy", "Gradle service permits one verified consumer at a time; unresolved work blocks reuse")
		}
	}
	storageRequest := parent.Request
	storageRequest.TTL = time.Until(*parent.Expires)
	if _, err := m.storageForRun(storageRequest); err != nil {
		return parent, err
	}
	return parent, nil
}

func (m *Manager) validateGradleRequest(r model.Request, run *RunRequest) error {
	if !isGradleProfile(r.Profile) {
		if r.ServiceLease != "" {
			return model.Err("profile_conflict", "service attachment requires the gradle-build profile")
		}
		return nil
	}
	if run == nil || r.Queue || r.StorageLease == "" || r.PortLease != "" || len(r.Ports) > 0 || len(r.Pools) > 0 || r.Peer != "" || r.Resources.Disk != 0 {
		return model.Err("profile_conflict", "initial Gradle profiles require local immediate supervised work and managed storage; queues, ports, pools and extra disk claims are unsupported")
	}
	if r.Profile == "gradle-service" {
		if r.ServiceLease != "" || r.Resources.Memory < 512<<20 {
			return model.Err("profile_conflict", "Gradle service requires at least 512MiB for its daemon/workers/startup probe and cannot attach another service")
		}
		return validateGradleService(run.Command, r.Resources.CPU)
	}
	if r.ServiceLease == "" || r.Resources.Memory < 128<<20 {
		return model.Err("profile_conflict", "Gradle build requires --service and at least 128MiB for its separate client JVM")
	}
	if err := validateGradleBuild(run.Command, r.Resources.CPU); err != nil {
		return err
	}
	parent, err := m.gradleParent(r, "")
	if err != nil {
		return err
	}
	if r.StorageLease == parent.Request.StorageLease {
		return model.Err("profile_conflict", "Gradle service state and consumer outputs require distinct storage leases so intermediate outputs can be reclaimed independently")
	}
	executable, err := gradleInstalledExecutable(run.Command[0])
	if err != nil || executable != parent.Workload.Executable {
		return model.Err("profile_conflict", "Gradle client must use its service's exact installed executable/distribution")
	}
	_, err = gradleProjectRoot(run.CWD)
	return err
}

func gradleChildDirectory(parent *os.File, name string, expected *gradleDirIdentity, create bool) (*os.File, gradleDirIdentity, error) {
	var none gradleDirIdentity
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, none, model.Err("unsafe_path", "invalid Gradle child directory")
	}
	if create {
		if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil {
			return nil, none, err
		}
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, none, err
	}
	f := os.NewFile(uintptr(fd), name)
	id, err := gradleDirStat(fd)
	if err == nil && expected != nil && id != *expected {
		err = model.Err("unsafe_path", "Gradle control subdirectory identity changed")
	}
	if err != nil {
		f.Close()
		return nil, none, err
	}
	return f, id, nil
}

func (m *Manager) prepareGradleLaunch(l *model.Lease, run *RunRequest) error {
	if !isGradleProfile(l.Request.Profile) {
		return nil
	}
	r := gradleRecord{Lease: l.ID, Storage: l.Request.StorageLease, Name: "workload-" + l.ID, Nonce: local.ID(), Phase: "creating", Children: map[string]gradleDirIdentity{}}
	executable, err := gradleInstalledExecutable(run.Command[0])
	if err != nil {
		return err
	}
	jvm, heap, metaspace, err := gradleJVMArgs(l.Request.Resources.Memory)
	if l.Request.Profile == "gradle-service" {
		jvm, heap, metaspace, err = gradleServiceJVMArgs(l.Request.Resources.Memory)
	}
	if err != nil {
		return err
	}
	r.Binding = model.WorkloadBinding{Adapter: "gradle-build", Role: "consumer", State: "preparing", Executable: executable, HeapBytes: heap, MetaspaceBytes: metaspace, JVMArgs: jvm}
	var parent model.Lease
	if l.Request.Profile == "gradle-service" {
		javaHome, err := gradleJavaHome()
		if err != nil {
			return err
		}
		r.Binding.Role, r.Binding.State, r.Binding.JavaHome = "service", "starting", javaHome
		r.ServiceHeap = heap
		r.ServiceMetaspace = metaspace
		r.Binding.WorkerLimit = int(l.Request.Resources.CPU)
		r.Binding.Detail = "Promise covers the owned foreground daemon, workers and bounded startup probe; 256MiB is allowed for the probe before sizing daemon JVM limits. Steady clients hold separate leases. JVM heap/metaspace are not total process memory."
	} else {
		parent, err = m.gradleParent(l.Request, l.ID)
		if err != nil {
			return err
		}
		cwd, err := gradleProjectRoot(run.CWD)
		if err != nil {
			return err
		}
		r.Binding.CWD, r.Binding.ServiceLease = cwd, parent.ID
		r.Binding.JavaHome, r.Binding.Home = parent.Workload.JavaHome, parent.Workload.Home
		r.Binding.WorkerLimit = parent.Workload.WorkerLimit
		r.Binding.ServiceProcess, r.Binding.ServiceStartMillis = parent.Workload.ServiceProcess, parent.Workload.ServiceStartMillis
		r.Binding.GradleVersion = parent.Workload.GradleVersion
		r.ServiceHeap = parent.Workload.HeapBytes
		r.ServiceMetaspace = parent.Workload.MetaspaceBytes
		r.Binding.Detail = "Promise covers this client JVM only; tasks and workers execute under the separately charged owned Gradle service. Missing completion or unexpected daemon evidence stays charged and blocks reuse."
	}
	if err := m.saveGradleRecord(l, r, "workload.preparing"); err != nil {
		return err
	}
	dir, id, err := m.openGradleDirectory(r.Storage, r.Name, nil, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	r.Identity = &id
	store, err := m.get(r.Storage)
	if err != nil {
		return err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(store.ManagedDir, r.Name))
	if err != nil {
		return err
	}
	names := []string{"receipts", "cache", "build"}
	if r.Binding.Role == "service" {
		names = []string{"receipts", "home", "probe", "cache"}
	}
	for _, name := range names {
		child, childID, err := gradleChildDirectory(dir, name, nil, true)
		if err != nil {
			return err
		}
		child.Close()
		r.Children[name] = childID
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if r.Binding.Role == "service" {
		r.Binding.Home, r.Binding.CWD = filepath.Join(path, "home"), filepath.Join(path, "probe")
		r.Binding.ProjectCache = filepath.Join(path, "cache")
		probe, _, err := gradleChildDirectory(dir, "probe", nil, false)
		if err != nil {
			return err
		}
		err = gradleWriteFile(probe, "settings.gradle", []byte("rootProject.name = 'arm-owned-service-probe'\n"))
		if err == nil {
			err = gradleWriteFile(probe, "build.gradle", []byte("tasks.register('armServiceProbe')\n"))
		}
		probe.Close()
		if err != nil {
			return err
		}
		run.Command = []string{executable, "--offline", "--foreground", "--gradle-user-home", r.Binding.Home, "-Dorg.gradle.jvmargs=" + strings.Join(jvm, " "), "-Dorg.gradle.java.home=" + r.Binding.JavaHome, "--console=plain"}
	} else {
		r.Binding.ProjectCache, r.Binding.BuildDirectory = filepath.Join(path, "cache"), filepath.Join(path, "build")
		r.Binding.State = "running"
		r.ExpectedPID = parent.Workload.ServiceProcess.PID
		program := gradleInitProgram(l.ID, r.Nonce, filepath.Join(path, "receipts"), r.Binding.CWD, r.Binding, r.ExpectedPID, r.Binding.ServiceStartMillis, r.ServiceHeap, r.ServiceMetaspace, false)
		if err := gradleWriteFile(dir, "init.gradle", []byte(program)); err != nil {
			return err
		}
		run.Command = append([]string{executable, "--daemon", "--no-parallel", "--no-configuration-cache", "--no-watch-fs", fmt.Sprintf("--max-workers=%d", r.Binding.WorkerLimit), "--gradle-user-home", r.Binding.Home, "--project-cache-dir", r.Binding.ProjectCache, "--init-script", filepath.Join(path, "init.gradle"), "-Dorg.gradle.jvmargs=" + strings.Join(parent.Workload.JVMArgs, " "), "-Dorg.gradle.java.home=" + r.Binding.JavaHome, "--console=plain"}, run.Command[1:]...)
	}
	run.CWD = r.Binding.CWD
	r.Phase = "prepared"
	return m.saveGradleRecord(l, r, "workload.prepared")
}

func applyGradleEnvironment(l model.Lease, cmd *exec.Cmd) {
	if l.Workload == nil {
		return
	}
	clean := make([]string, 0, len(cmd.Env)+3)
	for _, value := range cmd.Env {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "JAVA_HOME", "JAVA_OPTS", "GRADLE_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "GRADLE_USER_HOME":
			continue
		}
		clean = append(clean, value)
	}
	cmd.Env = append(clean, "JAVA_HOME="+l.Workload.JavaHome, "JAVA_OPTS="+strings.Join(l.Workload.JVMArgs, " "), "GRADLE_USER_HOME="+l.Workload.Home)
}
