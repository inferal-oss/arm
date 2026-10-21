package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func gradleJavaHome() (string, error) {
	home := os.Getenv("JAVA_HOME")
	if home == "" {
		if runtime.GOOS == "darwin" {
			// This OS inventory utility selects an already installed JDK. It
			// does not evaluate project code, install tools or start Gradle.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/libexec/java_home")
			var out gradleBoundedText
			cmd.Stdout = &out
			if err := cmd.Run(); err == nil && !out.overflow {
				home = strings.TrimSpace(out.text)
			}
		} else if path, err := exec.LookPath("java"); err == nil {
			if path, err = filepath.EvalSymlinks(path); err == nil {
				home = filepath.Dir(filepath.Dir(path))
			}
		}
	}
	return canonicalGradleJavaHome(home)
}

func canonicalGradleJavaHome(home string) (string, error) {
	if !filepath.IsAbs(home) {
		return "", model.Err("profile_conflict", "Gradle service requires an installed JDK; set JAVA_HOME before starting the ARM backend")
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", model.Err("profile_conflict", "Gradle JAVA_HOME cannot be resolved")
	}
	java, err := os.Stat(filepath.Join(resolved, "bin", "java"))
	if err != nil || !java.Mode().IsRegular() || java.Mode().Perm()&0111 == 0 {
		return "", model.Err("profile_conflict", "Gradle JAVA_HOME must contain an installed executable bin/java")
	}
	return resolved, nil
}

type gradleBoundedText struct {
	text     string
	overflow bool
}

func (b *gradleBoundedText) Write(p []byte) (int, error) {
	const limit = 16 << 10
	if len(p) > limit-len(b.text) {
		b.overflow = true
		return 0, fmt.Errorf("JDK inventory output exceeds bound")
	}
	b.text += string(p)
	return len(p), nil
}

func gradleJVMArgs(memory int64) ([]string, int64, int64, error) {
	if memory < 128<<20 {
		return nil, 0, 0, model.Err("profile_conflict", "Gradle JVMs require at least a 128MiB memory claim; heap/metaspace limits do not include all process memory")
	}
	heapMiB, metaspaceMiB := memory/(2<<20), memory/(4<<20)
	args := []string{
		"-Xms64m", fmt.Sprintf("-Xmx%dm", heapMiB), fmt.Sprintf("-XX:MaxMetaspaceSize=%dm", metaspaceMiB),
		"-Dfile.encoding=UTF-8", "-Duser.country=US", "-Duser.language=en", "-Duser.variant",
		"--add-opens=java.base/java.lang=ALL-UNNAMED", "--add-opens=java.base/java.lang.invoke=ALL-UNNAMED", "--add-opens=java.base/java.util=ALL-UNNAMED",
	}
	return args, heapMiB << 20, metaspaceMiB << 20, nil
}

const gradleProbeMemory int64 = 256 << 20

func gradleServiceJVMArgs(memory int64) ([]string, int64, int64, error) {
	if memory < 512<<20 {
		return nil, 0, 0, model.Err("profile_conflict", "Gradle service requires at least 512MiB including its startup probe")
	}
	// The probe runs concurrently with the daemon in the same charged group.
	// Budget it before sizing daemon limits rather than promising these bytes
	// to both JVMs. Their native/process memory still needs observed headroom.
	return gradleJVMArgs(memory - gradleProbeMemory)
}
