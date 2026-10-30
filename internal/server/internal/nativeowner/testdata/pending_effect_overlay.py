#!/usr/bin/env python3
"""Generate bounded opt-in native-effect controls; never execute Go or mutate source."""
from pathlib import Path
import argparse
import hashlib
import json


def digest(data):
    return hashlib.sha256(data).hexdigest()


def replace_once(text, before, after):
    if text.count(before) != 1:
        raise ValueError("native source boundary changed; review the control before generation")
    return text.replace(before, after, 1)


def generate(package, output):
    native = (package / "native.go").read_text()
    test = (package / "pending_effect_test.go").read_text()
    enabled = replace_once(test, "const expectNativeEffectPause = false", "const expectNativeEffectPause = true")
    enabled = replace_once(
        enabled,
        "func nativeEffectPauseChannels() (<-chan struct{}, func()) { return nil, func() {} }",
        "func nativeEffectPauseChannels() (<-chan struct{}, func()) {\n"
        "\treturn nativeProofReached, func() { nativeProofReleaseOnce.Do(func() { close(nativeProofContinue) }) }\n}",
    )
    output.mkdir(mode=0o700)  # Refuse an existing output rather than overwrite it.
    records = {}
    for name, hold_mutex in (("positive", False), ("held-mutex", True)):
        directory = output / name
        directory.mkdir(mode=0o700)
        source = replace_once(native, '\t"strings"\n', '\t"strings"\n\t"sync"\n')
        pause = "\tnativeProofOnce.Do(func() { close(nativeProofReached); <-nativeProofContinue })\n"
        if hold_mutex:
            pause = "\to.registry.mu.Lock()\n\tdefer o.registry.mu.Unlock()\n" + pause
        marker = '\to.progress("native_open_registered")\n'
        source = replace_once(source, marker, marker + pause)
        source += (
            "\nvar nativeProofOnce sync.Once\nvar nativeProofReleaseOnce sync.Once\n"
            "var nativeProofReached = make(chan struct{})\nvar nativeProofContinue = make(chan struct{})\n"
        )
        (directory / "native.go").write_text(source)
        (directory / "pending_effect_test.go").write_text(enabled)
        mapping = {
            "Replace": {
                str(package / "native.go"): str(directory / "native.go"),
                str(package / "pending_effect_test.go"): str(directory / "pending_effect_test.go"),
            }
        }
        (directory / "overlay.json").write_text(json.dumps(mapping, indent=2) + "\n")
        records[name] = {
            "native_sha256": digest(source.encode()),
            "test_sha256": digest(enabled.encode()),
            "expected_result": "pass" if not hold_mutex else "exact blocked-inspection assertion failure",
        }
    manifest = {
        "source_sha256": {"native.go": digest(native.encode()), "pending_effect_test.go": digest(test.encode())},
        "controls": records,
        "no_execution_or_source_mutation": True,
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package-dir", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--output", type=Path, required=True, help="new directory under an existing owned proof directory")
    args = parser.parse_args()
    manifest = generate(args.package_dir.resolve(), args.output.absolute())
    print(json.dumps({"controls": manifest["controls"], "no_execution_or_source_mutation": True}))


if __name__ == "__main__":
    main()
