# SPARQL extensions (draft)

Semblance's engines (Rust and WAT) understand a few things standard SPARQL 1.2
leaves out: extension functions, which the standard allows by IRI (SPARQL 1.1
§17.6), and *magic relations*, triple patterns the engine computes instead of
looking up. Queries using them are still syntactically standard SPARQL; only
the evaluation is ours. Oxigraph's JavaScript API has no hook for custom
functions, so on Oxigraph and N3 a rule that needs an extension is not
eligible and its function implementation runs instead.

Namespace: `PREFIX sx: <https://semblance.dev/ns/sparql#>`.

## Not extensions

Two limitations listed under `sem:functionOnly` need no engine support:

- **A query held in the data** (charts, Formula, Table, the result views). The
  runtime, or a function rule, writes the view's result into the graph as RDF
  rows; the rules that draw it are then plain SPARQL over those rows.
- **Paths over a predicate named in the data** (Breadcrumbs, Tree, link
  destinations). One hop with the predicate in a variable is a plain pattern
  (`?tree c:parentProperty ?prop . ?c ?prop ?p`); a helper rule mints a
  fixed-predicate edge (per tree, if trees can share nodes) and the walking
  rule follows it with `*`.

## Capabilities

Extensions come in groups, instances of `sx:Group`. A store says which it has; a
SPARQL rule says which it needs; the rule engine picks a rule only when the store
has all of them.

| group | IRI | contents |
|---|---|---|
| Math | `sx:Math` | `sx:log` … `sx:pi` |
| Text | `sx:Text` | `sx:codepoint` … `sx:fnv1a` |
| Locale | `sx:Locale` | `sx:formatNumber`, `sx:formatDateTime`, `sx:collated`, `sx:sortKey` |
| Matches | `sx:Matches` | `sx:matches`, `sx:chars` |
| JSON | `sx:Json` | `sx:json` |

- **Engine.** An optional export `extensions() -> i32`, a bit set in table
  order (Math = 1, Text = 2, Locale = 4, Matches = 8, JSON = 16). Absent means 0.
- **Store.** `GraphStore.extensions?: ReadonlySet<string>`, the group IRIs.
  Absent means none (N3, Oxigraph, hybrid).
- **Rule.** `sem:needsExtension sx:Math` (repeatable; domain `sem:Rule`, range
  `sx:Group`) on a rule with a `sem:construct`. (`sem:requires` is taken: it
  names a property the focus must have.) Rule selection goes by preference,
  then skips a rule whose groups the store lacks, so the next rule runs. A
  rendering whose SPARQL rule needs an extension still needs a function rule,
  for stores without it; `sem:functionOnly` is then dropped, and
  `test/implementations.ts` checks that both exist. A `beh:Reaction` may carry
  `sem:needsExtension` too, falling back to its `sem:function`.
- Queries using extensions are standard SPARQL syntax, so every parser (and
  `rdf-engine/host/parse-check.ts`) accepts them.
- An engine never rejects an `sx:` IRI it does not implement at prepare time:
  an unknown function is an expression error (standard behaviour) and an
  unknown magic relation is an ordinary predicate. The capability check is
  what keeps such rules off stores that lack them.

## Functions

Arguments are evaluated as in SPARQL; a wrong type, an unbound argument or a
value outside the domain is an expression error (unbound in BIND, false in
FILTER), as for the built-ins.

### `sx:Math`

All take numerics and return `xsd:double`.

| function | |
|---|---|
| `sx:log(x)`, `sx:log10(x)`, `sx:log2(x)` | natural, base 10, base 2; error for x ≤ 0 |
| `sx:exp(x)`, `sx:pow(x, y)`, `sx:sqrt(x)` | `sqrt` errors for x < 0 |
| `sx:sin(x)`, `sx:cos(x)`, `sx:tan(x)` | radians |
| `sx:asin(x)`, `sx:acos(x)`, `sx:atan(x)`, `sx:atan2(y, x)` | |
| `sx:pi()` | |

### `sx:Text`

Positions are 1-based and count code points, as in `SUBSTR`.

| function | returns |
|---|---|
| `sx:codepoint(s, i)` | `xsd:integer`, the code point at position `i`; error out of range |
| `sx:char(n)` | `xsd:string` of the one code point `n`; error if not a scalar value |
| `sx:utf8Hex(s)` | `xsd:string`: each UTF-8 byte of `s` as two lowercase hex digits, space-separated (`"€"` → `"e2 82 ac"`) |
| `sx:decodeURI(s)` | `xsd:string`: `%XX` sequences decoded as UTF-8; error if malformed |
| `sx:fnv1a(s)` | `xsd:string`: FNV-1a 32-bit hash of the UTF-8 bytes, 8 lowercase hex digits |

String results are simple literals (no language tag) whatever the input.

### `sx:Locale`

Formatting is done by the host's `Intl`, so results follow the host's locale
data. `lang` is a BCP 47 tag string; `options` an optional string of
`key=value` pairs separated by `;`, with the keys and values of the `Intl`
constructor's options (`"style=currency;currency=EUR;maximumFractionDigits=0"`).

| function | |
|---|---|
| `sx:formatNumber(n, lang [, options])` | `Intl.NumberFormat` |
| `sx:formatDateTime(dt, lang [, options])` | `Intl.DateTimeFormat`; `timeZone=…` in the options, else the host's zone |
| `sx:collated(s, lang)` | as an `ORDER BY` key, `s` is compared with `Intl.Collator(lang)`; anywhere else it is `s` |
| `sx:sortKey(s, lang)` | `xsd:string` that sorts by code point close to how `Intl.Collator(lang)` sorts `s`: case folded and accents stripped (NFD, combining marks removed, then lower-cased for `lang`). `Intl` has no collation-key API, so this is the weaker key; for `sem:orderKey`, where ORDER BY does not apply |

The time zone `sx:formatDateTime` uses when the options name none is a store
option (`timeZone`, default the host's own), so tests can fix it (`"UTC"`).

The engines reach `Intl` through one more `env` import (ABI.md):
`intl(op, in_ptr, in_len, out_ptr, out_cap) -> i32`, with UTF-8 arguments
separated by NUL; it returns the result's length, or minus the length needed
when `out_cap` is too small. `op` 0 is number formatting, 1 date-time
formatting, 2 comparison (returns -1, 0 or 1), 3 the sort key.

## Magic relations

A magic relation is a triple pattern whose subject is a list of inputs and
whose object is a list of outputs. Inputs must be bound when the pattern is
reached (the planner puts it after the patterns that bind them); outputs are
variables (or terms, which then filter). Each call yields zero or more rows.
They may appear wherever a triple pattern may, except in a property path or a
CONSTRUCT template.

### `sx:matches` (`sx:Matches`)

```sparql
(?text ?pattern) sx:matches (?match ?start ?g1 ?g2)
(?text ?pattern "i") sx:matches (?match ?start)
```

One row per non-overlapping match of the XPath regular expression `?pattern`
(flags as in `REGEX`) in `?text`, left to right; an empty match advances by one
code point. `?match` is the matched string, `?start` its 1-based position,
`?g1` … the capture groups (unbound for a group that did not take part). With an
alternation that also matches whatever falls between tokens, this is a
tokenizer:

```sparql
(?src '(#[^\n]*)|("[^"]*")|(\\?\\w+)|([A-Za-z_]\\w*)|(\\s+)|(.)') sx:matches (?tok ?at ?comment ?string ?var ?word ?space ?other)
```

### `sx:chars` (`sx:Matches`)

```sparql
(?text) sx:chars (?char ?index)
```

One row per code point.

### `sx:json` (`sx:Json`)

```sparql
(?payload) sx:json (?path ?parent ?key ?kind ?value)
```

Parses `?payload` as JSON (RFC 8259) and yields one row per value, in document
order: `?path` is its JSON Pointer (`""` for the root, `"/items/0/name"`),
`?parent` its parent's pointer (unbound for the root), `?key` the member name or
array index (string or integer), `?kind` one of `"object"`, `"array"`,
`"string"`, `"number"`, `"boolean"`, `"null"`, and `?value` the scalar's value
(`xsd:string`, `xsd:decimal` or `xsd:double`, `xsd:boolean`; unbound for objects,
arrays and null). Invalid JSON yields no rows. Rules mint IRIs from `?path`.

## Status

Draft for review; nothing implemented yet. The engines implement it in step
(Rust and WAT pass the same tests), after the current speed work lands.
