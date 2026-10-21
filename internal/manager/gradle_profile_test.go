package manager

import (
	"math"
	"strings"
	"testing"
)

func TestGradleServiceRejectsWrappersAndUncontrolledDaemonSettings(t *testing.T) {
	if err := validateGradleService([]string{"/installed/bin/gradle"}, 2); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{
		nil, {"./gradlew"}, {"sh", "gradle"}, {"gradle", "--foreground"},
		{"gradle", "--stop"}, {"gradle", "-g", "/user/.gradle"},
		{"gradle", "-Dorg.gradle.jvmargs=-Xmx8g"}, {"gradle", "build"},
	} {
		if err := validateGradleService(command, 2); err == nil {
			t.Fatal("uncontrolled service configuration accepted", command)
		}
	}
	for _, cpu := range []float64{0, .5, -1, math.NaN(), math.Inf(1)} {
		if err := validateGradleService([]string{"gradle"}, cpu); err == nil {
			t.Fatal("invalid daemon/worker CPU budget accepted", cpu)
		}
	}
}

func TestGradleBuildCannotOverrideServiceCompatibilityOrWorkers(t *testing.T) {
	if err := validateGradleBuild([]string{"gradle", "--offline", ":app:assemble", "check", "--stacktrace"}, .25); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		nil, {"--offline"}, {"--max-workers=99", "build"}, {"--max-w=99", "build"},
		{"-Dorg.gradle.java.home=/other/jdk", "build"}, {"--system-prop", "org.gradle.jvmargs=-Xmx9g", "build"},
		{"-g/user/home", "build"}, {"--gradle-user-home=/other", "build"}, {"--project-cache-dir=/other", "build"},
		{"--daemon", "build"}, {"--no-daemon", "build"}, {"--stop"}, {"--status"}, {"--foreground"},
		{"-I", "/user/init.gradle", "build"}, {"--init-script=/user/init.gradle", "build"},
		{"-p", "/other/project", "build"}, {"--continuous", "build"}, {"--priority=low", "build"},
		{"--parallel", "build"}, {"--configuration-cache", "build"}, {"--", "build"}, {"build\nforged"},
	} {
		command := append([]string{"gradle"}, args...)
		if err := validateGradleBuild(command, .25); err == nil || !strings.Contains(err.Error(), "profile_conflict") {
			t.Fatal("uncontrolled build configuration accepted", command, err)
		}
	}
}
