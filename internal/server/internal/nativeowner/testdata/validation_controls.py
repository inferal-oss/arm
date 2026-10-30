#!/usr/bin/env python3
"""Generate explicit ARM-owned validation overlays without executing Go."""
from pathlib import Path
import argparse
import hashlib
import json


def once(text, before, after):
    if text.count(before) != 1:
        raise ValueError("original source boundary differs; review before generation")
    return text.replace(before, after, 1)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def generate(package, output):
    originals = {name: (package / name).read_text() for name in
                 ["owner.go", "validation.go", "validation_pause_test.go"]}
    output.mkdir(mode=0o700)  # Existing output is refused.
    records = []
    for name, mode, held, omitted in [
        ("pending_positive", "pause", False, False),
        ("pending_held_mutex", "pause", True, False),
        ("panic_positive", "panic", False, False),
        ("panic_omitted_guard", "panic", False, True),
        ("goexit_positive", "goexit", False, False),
        ("goexit_omitted_guard", "goexit", False, True),
    ]:
        owner = once(originals["owner.go"], '\t"sync"\n', '\t"sync"\n\t"sync/atomic"\n')
        if mode == "goexit":
            owner = once(owner, '\t"sync"\n', '\t"runtime"\n\t"sync"\n')
        hook = "\tresidentValidationProofPause()\n"
        if held:
            hook = "\tfunc() {\n\t\to.mu.Lock()\n\t\tdefer o.mu.Unlock()\n\t\tresidentValidationProofPause()\n\t}()\n"
        owner = once(owner, '\tn.progress("native_validation_pending")\n', '\tn.progress("native_validation_pending")\n' + hook)
        if omitted:
            start = owner.index("func (o *nativeOwner) validateEffect(")
            end = owner.index("// Low-level FD operation", start)
            body = once(owner[start:end], "\tdefer o.finishEffect(op, &normal, &resultErr)\n", "\tif residentValidationProofArmed.Load() {\n\t\tdefer func() { _ = normal }()\n\t} else {\n\t\tdefer o.finishEffect(op, &normal, &resultErr)\n\t}\n")
            owner = owner[:start] + body + owner[end:]
        abnormal = 'panic("original validation fixture panic")' if mode == "panic" else "runtime.Goexit()" if mode == "goexit" else ""
        owner += """
var residentValidationProofArmed atomic.Bool
var residentValidationProofCalls atomic.Int64
var residentValidationProofReached = make(chan struct{})
var residentValidationProofQueued = make(chan struct{}, maxNativeValidationWaiters)
var residentValidationProofContinue = make(chan struct{})
var residentValidationProofRelease sync.Once
func residentValidationProofPause() {
 residentValidationProofCalls.Add(1)
 if !residentValidationProofArmed.CompareAndSwap(true,false) { return }
 close(residentValidationProofReached)
 <-residentValidationProofContinue
 ABNORMAL
}
""".replace("ABNORMAL", abnormal)
        validation = once(originals["validation.go"], "\t\t\t\to.validationWaiters[waiter] = struct{}{}\n", "\t\t\t\to.validationWaiters[waiter] = struct{}{}\n\t\t\t\tresidentValidationProofQueued <- struct{}{}\n")
        tests = once(originals["validation_pause_test.go"], "const expectNativeValidationPause = false", "const expectNativeValidationPause = true")
        old = '\treturn "", func() {}, nil, nil, func() {}, func() int64 { return 0 }'
        new = ('\treturn "' + mode + '", func(){residentValidationProofArmed.Store(true)}, residentValidationProofReached, residentValidationProofQueued, '
               'func(){residentValidationProofRelease.Do(func(){close(residentValidationProofContinue)})}, residentValidationProofCalls.Load')
        tests = once(tests, old, new)
        directory = output / name
        directory.mkdir(mode=0o700)
        sources = {"owner.go": owner, "validation.go": validation, "validation_pause_test.go": tests}
        for filename, source in sources.items():
            (directory / filename).write_text(source)
        (directory / "overlay.json").write_text(json.dumps({"Replace": {str(package / n): str(directory / n) for n in sources}}, indent=2) + "\n")
        root = "TestOriginalNativeValidationQueuedCancellationCapacityAndFreshWake" if mode == "pause" else "TestOriginalNativeValidationAbnormalRetainsOriginalAndRefusesReplacement"
        negative = held or omitted
        records.append({"name": name, "root": root, "expected_exit": 1 if negative else 0,
                        "expected_result": "fail" if negative else "pass",
                        "exact_assertion": "inspection blocked behind original borrower validation" if held else "original abnormal native validation was forgotten" if omitted else None,
                        "source_sha256": {n: sha(s.encode()) for n, s in sources.items()}})
    manifest = {"base_sha256": {n: sha(s.encode()) for n, s in originals.items()}, "cases": records,
                "Go_execution": False, "production_source_mutated": False,
                "scope": "actual original ARM native validation and wait admission; whole owned fixture child; no product Closed"}
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    manifest = generate(args.package_dir.resolve(), args.output.absolute())
    print(json.dumps({"cases": len(manifest["cases"]), "Go_execution": False, "source_mutation": False}))
