package manager

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/usage"
)

func TestIndependentGradleRecoveredIdentityNeverAuthorizesSignal(t *testing.T) {
	foreign := exec.Command("/bin/sleep", "60")
	foreign.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { foreign.Process.Kill(); foreign.Wait() }()
	identity, err := usage.Capture(foreign.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	m := newManager(t)
	lease, err := m.Acquire(request("independent-recovered-gradle"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// A durable identity can be exact and still confer no live signalling
	// authority once the manager-owned job/child relationship has been lost.
	lease.Request.Profile = "gradle-service"
	lease.PID = identity.PID
	lease.Process = &identity
	lease.State = "needs_attention"
	lease.Outcome = "supervision_lost"
	lease.Revision++
	if err := m.put(lease, "fixture.recovered"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reconcile(lease.ID); errorCode(err) != "needs_attention" {
		t.Fatal("recovered Gradle process was adopted", err)
	}
	if _, err := m.End(lease.ID, "revoked"); errorCode(err) != "needs_attention" {
		t.Fatal("recovered Gradle process was ended", err)
	}
	if current, err := usage.Capture(identity.PID); err != nil || current != identity {
		t.Fatal("observation/revocation touched an unrelated process", err)
	}
	current, _ := m.Inspect(lease.ID)
	if current.State != "needs_attention" {
		t.Fatal("recovered obligation silently released", current)
	}
}
func TestIndependentGradleEnvironmentCannotInjectJVMOrHomeOptions(t *testing.T) {
	binding := &model.WorkloadBinding{JavaHome: "/pinned/jdk", Home: "/owned/gradle", JVMArgs: []string{"-Xmx128m", "-Dfile.encoding=UTF-8"}}
	command := exec.Command("/bin/false")
	command.Env = []string{"JAVA_HOME=/other/jdk", "JAVA_OPTS=-javaagent:/unowned/agent.jar", "GRADLE_OPTS=-Dorg.gradle.jvmargs=-Xmx99g", "JAVA_TOOL_OPTIONS=-javaagent:/other.jar", "JDK_JAVA_OPTIONS=-Xmx99g", "_JAVA_OPTIONS=-Xmx99g", "GRADLE_USER_HOME=/shared/home", "PRESERVE_ME=ordinary"}
	applyGradleEnvironment(model.Lease{Workload: binding}, command)
	values := map[string]string{}
	for _, entry := range command.Env {
		key, value, _ := strings.Cut(entry, "=")
		if _, exists := values[key]; exists {
			t.Fatal("duplicate environment can change JVM precedence", key)
		}
		values[key] = value
	}
	for _, key := range []string{"GRADLE_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"} {
		if _, exists := values[key]; exists {
			t.Fatal("uncontrolled JVM environment remained", key)
		}
	}
	if values["JAVA_HOME"] != binding.JavaHome || values["GRADLE_USER_HOME"] != binding.Home || values["JAVA_OPTS"] != strings.Join(binding.JVMArgs, " ") || values["PRESERVE_ME"] != "ordinary" {
		t.Fatalf("effective environment mismatch %+v", values)
	}
}
