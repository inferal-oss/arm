# Embedded workload cards

Cards in `cards/*.md` are shipped inside ARM. They explain implemented behavior
and its limits; they are not executable policies or workload detectors.

`make build` validates the catalog before compiling ARM. `go test ./knowledge`
also validates every embedded card. `go run ./scripts/check-knowledge` runs the
same gate directly. A bare `go build` only compiles; use the build gate when
changing cards. No documentation download or external language is needed at
runtime.

After changing metadata or adding/removing cards, run `go generate ./knowledge`
to update the embedded `index.json`. The build gate rejects a stale or invalid
index as well as invalid card sources; normal builds never rewrite it.

Each card starts with YAML frontmatter containing exactly `schema_version: 1`,
`id`, `title`, `tags`, `resources` and `adapter`. IDs and adapter names are
lowercase hyphenated identifiers. Tags and resources are unique, nonempty flow
sequences of at most 16 values, for example `[rust, cargo]`. Identifiers are at
most 64 bytes; titles are at most 200 bytes. Metadata excludes control and
formatting characters. Resources are drawn from `cpu`, `memory`,
`storage`, `gpu`, `network`, `ports` and `custom`. The adapter must be registered
in the code used by request admission; an unknown binding fails validation.

The parser deliberately supports only this small YAML subset: top-level scalars,
single-quoted strings (double an embedded single quote), JSON double-quoted
strings and flow sequences of identifier strings. Nested mappings, anchors,
tags, comments, implicit typing and multiline scalars are not supported. Quote
titles containing YAML punctuation. Cards must be UTF-8, no larger than 64 KiB,
and contain a nonempty Markdown body. Unknown/duplicate fields, duplicate card
IDs, malformed metadata or missing implementation bindings fail the gate.

`arm agents` lists card metadata. `arm agents card NAME` returns a card's
Markdown in a terminal and structured metadata plus Markdown when piped or with
`--json`. These commands work offline without creating state. Web/TUI discovery
can consume the same catalog when those views are implemented.
