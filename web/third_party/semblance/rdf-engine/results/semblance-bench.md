# Semblance bench on the engine builds

`bench/node-one.ts` cells (engine ms; mean of 15 updates, initial render a
single run), Node 26, darwin/arm64, 2026-10-24. Rust with prepared queries
(`GraphStore.constructFor`, `?this` bound instead of substituted as text).

### SPARQL rules

| store | items | initial render | toggle | rename | reorder | add | type in field |
|---|---:|---:|---:|---:|---:|---:|---:|
| rust · sparql · bulk | 100 | 35.3 | 0.07 | 0.05 | 0.05 | 0.13 | 0.05 |
| oxigraph · sparql · bulk | 100 | 193.8 | 0.45 | 0.45 | 0.56 | 1.61 | 0.90 |
| hybrid · sparql · bulk | 100 | 80.9 | 0.51 | 0.27 | 0.28 | 3.88 | 3.83 |
| rust · sparql · bulk | 1000 | 106.9 | 0.06 | 0.04 | 0.07 | 1.19 | 0.05 |
| rust · sparql · per-focus | 1000 | 108.4 | 0.06 | 0.07 | 0.29 | 0.37 | 0.05 |
| oxigraph · sparql · bulk | 1000 | 860.5 | 0.52 | 0.55 | 0.66 | 3.17 | 0.85 |
| oxigraph · sparql · per-focus | 1000 | 1004.0 | 0.34 | 0.28 | 1.13 | 2.22 | 0.70 |
| hybrid · sparql · bulk | 1000 | 386.4 | 0.41 | 0.42 | 0.33 | 1.88 | 0.76 |

### Function rules

| store | items | initial render | toggle | rename | reorder | add | type in field |
|---|---:|---:|---:|---:|---:|---:|---:|
| rust | 1000 | 99.6 | 0.09 | 0.05 | 0.05 | 0.16 | 0.04 |
| n3 | 1000 | 127.1 | 0.09 | 0.12 | 0.07 | 0.44 | 0.06 |
| hybrid | 1000 | 245.4 | 0.10 | 0.15 | 0.13 | 0.51 | 0.10 |
| oxigraph | 1000 | 792.4 | 0.27 | 0.36 | 0.44 | 2.14 | 0.87 |

Single-run numbers; sub-millisecond cells vary by ±50% between runs.
