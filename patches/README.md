# Browser Screen Sharing patches

`novnc-standard-viewer.patch` is the ARM adaptation of the `ard/` tree from
[noVNC-ARD](https://github.com/peetinc/noVNC-ARD) commit
`d699293635083c9e0c30c8f9480993865653772a`. It contains import-path,
Standard-mode, pointer, and decoder changes. It applies with `patch -p1` from
the root of a copy of that revision. ARM vendors the result under
`internal/server/web/ard/` alongside noVNC 1.7.0 under `web/novnc/`.

`novnc-clipboard-api.patch` is separate and applies with `patch -p1` from the
ARM repository root after the Standard viewer changes. It adds explicit,
bounded text pasteboard operations and removes automatic clipboard reads.

Both patches were checked with `patch --dry-run`; the clipboard patch was
tested against the pre-clipboard ARM file and the viewer patch against the
named upstream revision. Browser unit tests cover packet format and bounds.
A UTM guest web viewer accepted clicks, and an idle delayed guest-to-host
click opened the host Apple menu. Sustained control from an independent
physical client and a clipboard round trip still need acceptance tests.
Temporary pointer diagnostics are excluded from both patches.

Independent final composition reproduces both patches with `--fuzz=0` and
compares every resulting ARD runtime file. The vendored `ard/LICENSE.txt` is
the pinned upstream repository's root MPL 2.0 license. The noVNC runtime base
is the 1.7.0 release archive, with these explicit local distribution changes:

- `vendor/pako/` is relocated to `deps/pako/`, preserving all 14 files and their
  license byte-for-byte. Go module ZIPs omit directories named `vendor`, so the
  original layout silently lost these browser dependencies in public installs.
- Only the relative `../vendor/pako/` import prefixes in `core/deflator.js` and
  `core/inflator.js` become `../deps/pako/`. Compression algorithms are unchanged.
- AUTHORS retains the existing normalization of one trailing space.

To reproduce the distribution transformation from an extracted noVNC 1.7.0
runtime tree, run the following inside that copied tree before comparing its
runtime files with `internal/server/web/novnc/`:

```sh
mkdir -p deps
mv vendor/pako deps/pako
rmdir vendor
python3 - <<'PYCODE'
from pathlib import Path
for name in ('core/deflator.js', 'core/inflator.js'):
    path = Path(name)
    path.write_bytes(path.read_bytes().replace(b'../vendor/pako/', b'../deps/pako/'))
PYCODE
```

This packaging transform is separate from the two ARD patches above and changes
neither their application order nor their authentication, decoder, pointer or
clipboard semantics. The upstream noVNC license text remains unchanged historical
material; the relocated pako license is at `deps/pako/LICENSE`.
