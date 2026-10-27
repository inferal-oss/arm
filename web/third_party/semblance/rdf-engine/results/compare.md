# Engine comparison

## 2026-10-24 · Node v26.10.0 (darwin/arm64)

100,000 quads; 20,000 lookups of (s, p, ?) returning about 0.5 quads each. µs per op, best of 3.

| engine | wasm KiB (raw / gzip / brotli) | load N-Triples | add | match(s, p, ?) | match + read objects | remove + add | memory |
|---|---:|---:|---:|---:|---:|---:|---:|
| rust | 262.1 / 102.6 / 86.4 | 1.47 | 0.20 | 0.42 | 0.92 | 0.48 | 30.3 MiB |
| wat | 59.6 / 27.7 / 24.8 | 0.49 | 0.06 | 0.34 | 0.84 | 0.15 | 26.7 MiB |
| oxigraph | 4226.8 / 1476.7 / 1000.2 | 1.76 | 7.24 | 8.81 | 12.2 | 9.14 |  |
| n3 | JS | 3.71 | 0.76 | 2.32 | 2.25 | 1.53 |  |
