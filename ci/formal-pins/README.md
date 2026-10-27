# Locked TLC development input

This unmodified public TLC archive matches `formal/tools.lock.json` exactly.
It provides reproducible diagnostic CI bootstrap after the upstream asset at the
same prerelease URL changed. The ordinary installer still checks file type,
link count, byte count, SHA-256 and installed inventory. Tool health does not
establish a model, trace or proof result.

TLC is distributed by the [TLA+ tools project](https://github.com/tlaplus/tlaplus).
Its retained manifest identifies source commit
[d2e1472884d292df7b0ed788712b1af262edc16e](https://github.com/tlaplus/tlaplus/tree/d2e1472884d292df7b0ed788712b1af262edc16e).
Original dependency license and NOTICE files remain inside the unchanged JAR;
extracted license notices are also provided in `notices/`. The manifest records
the original public URL and exact content identity. The complete existing tool
lock is retained for provenance; only the TLC archive is included here.
No ARM runtime installation or service is included.
