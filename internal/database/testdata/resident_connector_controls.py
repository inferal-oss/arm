#!/usr/bin/env python3
"""Generate explicit owned-wrapper test overlays; no Go execution or source mutation."""
from pathlib import Path
import argparse
import hashlib
import json


def digest(data):
    return hashlib.sha256(data).hexdigest()


def replace_once(text, before, after):
    if text.count(before) != 1:
        raise ValueError("original wrapper boundary changed; review before generation")
    return text.replace(before, after, 1)


def generate(package, output, template):
    original = (package / "resident_connector.go").read_text()
    original_test = (package / "resident_connector_test.go").read_text()
    extra_tests = template.read_text()
    extra_tests = extra_tests[extra_tests.index("// Explicit owned-wrapper"):]
    augmented_test = replace_once(original_test, '\t"testing"\n', '\t"testing"\n\t"time"\n') + "\n" + extra_tests
    cases = [
        ("pending_positive", "pending", False, False),
        ("pending_held_mutex", "pending", True, False),
        ("panic_positive", "panic", False, False),
        ("panic_omitted_guard", "panic", False, True),
        ("goexit_positive", "goexit", False, False),
        ("goexit_omitted_guard", "goexit", False, True),
    ]
    roots = {
        "pending": "TestResidentStandardPendingOriginalConnectAllowsInspectionAndRefusesAdmission",
        "panic": "TestResidentStandardOriginalConnectPanicRetainsCauseAndRefusesAdmission",
        "goexit": "TestResidentStandardOriginalConnectGoexitRetainsCallAndRefusesAdmission",
    }
    output.mkdir(mode=0o700)  # Existing output is refused, never overwritten.
    records = []
    for name, mode, held_mutex, omitted_guard in cases:
        source = original
        if mode == "pending":
            hook = "\tclose(residentConnectProofReached)\n\t<-residentConnectProofContinue\n"
            if held_mutex:
                hook = "\to.mu.Lock()\n\tdefer o.mu.Unlock()\n" + hook
        elif mode == "panic":
            hook = '\tpanic("original public Connect fixture panic")\n'
        else:
            source = replace_once(source, '\t"fmt"\n', '\t"fmt"\n\t"runtime"\n')
            hook = "\truntime.Goexit()\n"
        marker = "\treturned, resultErr = c.original.Connect(ctx)\n"
        source = replace_once(source, marker, hook + marker)
        if omitted_guard:
            source = replace_once(source, "\tdefer o.finishOriginalConnect(call, &normal, &returned, &resultErr)\n", "\tdefer func() { _ = normal }()\n")
        source += ('\nconst residentConnectProofMode = "' + mode + '"\n\n'
                   "var residentConnectProofReached = make(chan struct{})\n"
                   "var residentConnectProofContinue = make(chan struct{})\n")
        directory = output / name
        directory.mkdir(mode=0o700)
        (directory / "resident_connector.go").write_text(source)
        (directory / "resident_connector_test.go").write_text(augmented_test)
        mapping = {"Replace": {
            str(package / "resident_connector.go"): str(directory / "resident_connector.go"),
            str(package / "resident_connector_test.go"): str(directory / "resident_connector_test.go"),
        }}
        (directory / "overlay.json").write_text(json.dumps(mapping, indent=2) + "\n")
        negative = held_mutex or omitted_guard
        records.append({
            "name": name, "root": roots[mode], "expected_exit": 1 if negative else 0,
            "expected_result": "fail" if negative else "pass",
            "exact_assertion": ("inspection blocked behind original public Connect" if held_mutex else
                                "original public Connect abnormal state was forgotten" if omitted_guard else None),
            "source_sha256": {"resident_connector.go": digest(source.encode()), "resident_connector_test.go": digest(augmented_test.encode())},
            "official_driver_modified": False,
        })
    manifest = {
        "base_sha256": {"resident_connector.go": digest(original.encode()), "resident_connector_test.go": digest(original_test.encode())},
        "cases": records, "base_source_mutated": False, "Go_executed": False,
        "scope": "owned wrapper before official Connect only; no hidden-native or Closed witness",
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    manifest = generate(args.package_dir.resolve(), args.output.absolute(), Path(__file__).with_name("resident_connector_controls.go.txt"))
    print(json.dumps({"cases": len(manifest["cases"]), "Go_execution": False, "source_mutation": False}))
