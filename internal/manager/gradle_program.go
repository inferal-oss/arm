package manager

import (
	"encoding/base64"
	"fmt"

	"github.com/yrashk/arm/internal/model"
)

type gradleEvidence struct {
	Nonce        string `json:"nonce"`
	Lease        string `json:"lease"`
	Phase        string `json:"phase"`
	PID          int    `json:"pid"`
	StartMillis  int64  `json:"start_millis"`
	JavaHome     string `json:"java_home"`
	GradleHome   string `json:"gradle_home"`
	Version      string `json:"version"`
	MaxHeap      int64  `json:"max_heap"`
	MetaspaceMax int64  `json:"metaspace_max"`
	Workers      int    `json:"workers"`
	Failure      bool   `json:"failure"`
	Detail       string `json:"detail"`
}

func gradleQuote(value string) string {
	// Only the fixed base64 alphabet enters source. Paths with quotes, dollar
	// interpolation, backslashes, Unicode or line breaks remain literal data.
	return "new String('" + base64.StdEncoding.EncodeToString([]byte(value)) + "'.decodeBase64(), java.nio.charset.StandardCharsets.UTF_8)"
}

// The first action checks the executing daemon before settings/project code.
// Receipt mismatch is evidence of uncertainty, never authority to signal PID.
// Only generated files and the selected managed output directory are touched.
func gradleInitProgram(lease, nonce, receipts, cwd string, binding model.WorkloadBinding, expectedPID int, expectedStartMillis, expectedHeap, expectedMetaspace int64, probe bool) string {
	return fmt.Sprintf(`import groovy.json.JsonOutput
import java.nio.file.Files
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.PosixFilePermissions
import java.nio.channels.FileChannel
import java.nio.ByteBuffer
import java.nio.charset.StandardCharsets
import java.security.MessageDigest

def armReceipts = new File(%s).toPath()
def armEvidence = { String phase, boolean failure, String detail ->
    def value = [nonce:%s, lease:%s, phase:phase, pid:ProcessHandle.current().pid(), start_millis:ProcessHandle.current().info().startInstant().map { it.toEpochMilli() }.orElse(0L),
        java_home:new File(System.getProperty('java.home')).canonicalPath,
        gradle_home:gradle.gradleUserHomeDir.canonicalPath,
        version:gradle.gradleVersion, max_heap:Runtime.runtime.maxMemory(), metaspace_max:java.lang.management.ManagementFactory.memoryPoolMXBeans.find { it.name == 'Metaspace' }?.usage?.max ?: -1L,
        workers:gradle.startParameter.maxWorkerCount, failure:failure, detail:detail]
    def bytes = JsonOutput.toJson(value).getBytes(StandardCharsets.UTF_8)
    def tmp = Files.createTempFile(armReceipts, '.receipt-', '.tmp', PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString('rw-------')))
    def channel = FileChannel.open(tmp, StandardOpenOption.WRITE)
    try {
        def buffer = ByteBuffer.wrap(bytes)
        while (buffer.hasRemaining()) channel.write(buffer)
        channel.force(true)
    } finally { channel.close() }
    // Publish exclusively: ATOMIC_MOVE may replace an existing destination.
    Files.createLink(armReceipts.resolve(phase + '.json'), tmp)
    Files.delete(tmp)
}
def armActual = ProcessHandle.current().pid()
def armStart = ProcessHandle.current().info().startInstant().map { it.toEpochMilli() }.orElse(0L)
def armMetaspace = java.lang.management.ManagementFactory.memoryPoolMXBeans.find { it.name == 'Metaspace' }?.usage?.max ?: -1L
def armVersion = %s
if (armActual != %dL || armStart <= 0L || (%dL != 0L && armStart != %dL) || new File(System.getProperty('java.home')).canonicalPath != %s || gradle.gradleUserHomeDir.canonicalPath != %s || Runtime.runtime.maxMemory() > %dL || armMetaspace <= 0L || armMetaspace > %dL || (armVersion != '' && gradle.gradleVersion != armVersion)) {
    armEvidence('mismatch', true, 'Unexpected Gradle daemon identity/JDK/home/heap; no project tasks authorized; do not infer reclamation')
    throw new GradleException('ARM Gradle daemon compatibility mismatch before project settings/tasks')
}
if (gradle.startParameter.currentDir.canonicalPath != %s) {
    armEvidence('mismatch', true, 'Unexpected Gradle project directory')
    throw new GradleException('ARM Gradle project directory mismatch')
}
gradle.startParameter.maxWorkerCount = %d
gradle.startParameter.parallelProjectExecutionEnabled = false
armEvidence('started', false, '')
gradle.beforeSettings { settings ->
    if (settings.settingsDir.canonicalPath != %s) {
        throw new GradleException('ARM Gradle nested/included settings are unsupported by this profile')
    }
}
gradle.projectsLoaded {
    if (!gradle.includedBuilds.empty) {
        throw new GradleException('ARM Gradle composite/included builds are unsupported by this profile')
    }
}
gradle.projectsEvaluated {
    if (gradle.startParameter.maxWorkerCount != %d || gradle.startParameter.parallelProjectExecutionEnabled) {
        gradle.startParameter.maxWorkerCount = %d
        gradle.startParameter.parallelProjectExecutionEnabled = false
        throw new GradleException('ARM Gradle worker/parallel settings were overridden; project tasks were not authorized')
    }
}
if (!%t) {
    gradle.beforeProject { project ->
        def name = project.path == ':' ? 'root' : 'p-' + MessageDigest.getInstance('SHA-256').digest(project.path.getBytes(StandardCharsets.UTF_8)).encodeHex().toString().substring(0, 16)
        project.layout.buildDirectory.set(new File(%s, name))
        project.layout.buildDirectory.disallowChanges()
    }
}
gradle.buildFinished { result -> armEvidence('finished', result.failure != null, '') }
`, gradleQuote(receipts), gradleQuote(nonce), gradleQuote(lease), gradleQuote(binding.GradleVersion), expectedPID, expectedStartMillis, expectedStartMillis,
		gradleQuote(binding.JavaHome), gradleQuote(binding.Home), expectedHeap, expectedMetaspace,
		gradleQuote(cwd), binding.WorkerLimit, gradleQuote(cwd), binding.WorkerLimit, binding.WorkerLimit, probe, gradleQuote(binding.BuildDirectory))
}
