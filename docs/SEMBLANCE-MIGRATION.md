# Semblance product web migration

The frozen author snapshot is `dbfa5d6ae48da454e3577b9ee8b26c2ddc97569d`,
based on `21240eb`. Its evidence below records that author's source state; it
does not establish acceptance of the current canonical composition. Independent
review composes the owned UI onto `343b1b3`, preserving the accepted backend,
TLS, API, availability and module behavior. Stock Semblance is the authorized default. Technical integration still requires
the final composed native and browser gates.

The review relocates rebuild sources to `web/third_party/semblance` so Go module
ZIPs retain them, excludes exactly five unused upstream agent-tool files, pins
all runtime notices and closes screen error-to-graph privacy paths. Fresh builds,
current native/server tests and rendered browser evidence remain required for
these changed bytes. No unpublished upstream overlay is an accepted release.

## Architecture and provenance

`internal/server/web/semblance/app.ttl` defines stock component instances,
Semblance as the default theme, Segmented navigation and Switch view state. The
host writes bounded backend snapshots into graph component data. Stock SPARQL
renderings and reactions own DOM, ordinary input state and events. Host effects
perform the existing authenticated backend protocol; rendering or preparing a
review cannot authorize a mutation. `graph-ui.mjs` constructs RDF instances and
supports controlled IO callbacks after a graph event.

`web/third_party/semblance` starts from a clean source archive from
`inferal-oss/semblance` commit
`0db58805c6f70e4ef281c17ecf2b53ad947dd984`, under MIT OR Apache-2.0.
The provenance manifest records the original base-file hashes and actual combined file hashes. Provisional overlays #154/#155/#157/#166 are present and remain unaccepted upstream.
The subsequent native option selection correction is separately hashed in
`review_overlays`; the original 369 base and 373 combined reference hashes remain. The WAT engine is rebuilt from that same source. Node/Vite are development-only;
Go embeds `web/assets` and serves the local bundle and WASM. No framework CDN or
runtime package fetch is used. Existing noVNC/ARD assets retain their separately
embedded licensed delivery and feature semantics.

Mac framebuffer, credentials, the separate-account username and clipboard text
have narrowly scoped `ext:Island` integrations. The framebuffer is noVNC-owned.
Private text remains in ephemeral native fields, without RDF values or LDES
payloads; every surrounding control/dialog/action is stock. See
[the viewer integration contract](../internal/server/web/semblance/SCREEN-INTEGRATION.md).

## Coverage and gates

| Surface | Graph components | Required remaining evidence |
| --- | --- | --- |
| Shell/theme/navigation | Stack, Heading, Badge, Choice, Segmented, Switch | URL/back/forward, focus, desktop/narrow, saved theme |
| Monitor | Card, Heading, Progress, chart/data points, Checkbox, AccordionItem | All-core/remaining/IO parity, zero/gaps, pause/focus, live/stale/disconnect |
| Resources | Card, Text, Button, AccordionItem | Independent native budget aging, exact consumers, physical/promised distinction |
| Pool hierarchy/editor/journal | Card/Stack/Choice/fields/Dialog/Code | Real RDF/DOM confirmation, hierarchy paging, recovery and exact int64 |
| Leases/detail/check-ins | Card, Dialog, fields, Code | Exact revision/consumer confirmation, unknown outcome, feature gates |
| Messages/replies/broadcasts | MessageList with component bodies, Choice, TextArea, Card, Code, ReadingMeasure | Exact sessions/targets, durable key recovery and explicit retry |
| Activity/live feed | Timeline, side-attached Drawer, Toolbar, Checkbox | Separate durable history/feed, bounded updates, focus/read preservation |
| Host | Card, DescriptionList, Grid, AccordionItem, Code | Scoped metadata/capabilities/ports and unknown values |
| Mesh lifecycle | Card, Dialog, Choice, fields, Code | Connect/reconnect/install/Stop/uninstall/finish/cancel/handoff/receipt walkthrough |
| TCP forwarding | Fields, Card, explicit actions | Private token lifecycle and original unresolved key fence |
| Kubernetes inventory | Choice, fields, Card | Context/namespace/UID stale selection; selection never applies a Pod |
| Remote node window | Card, Text, Dialog, Code | Exact bound context/grants, native freshness, no local fallback |
| Screen Sharing | Graph controls plus narrow islands | Real RDF secret exclusion, keyboard/clipboard/fullscreen, actual Mac gate |
| Embedded delivery | Vite bundle plus WAT, Go embed/HTTP | Reproducible lock/build, carrier/offline assets, CSP/license review |

## Dependencies and upstream inventory

The pool and lease/message commits depend only on the graph handle contract and
unchanged `pool-model`, `pool-operations`, `lease-actions`, `lease-reviews` and
message operation policies. Mesh uses the baseline `mesh_operator_*`,
`mesh_forward_*`, `node_*` and read-only `kube_*` contracts. Other portfolios own
new forwarding/public sync/Kubernetes/VNC semantics and must supply their exact
reviewed API changes centrally. No preview gains execution rights here.

Upstream search included current main, component/layer ontologies, gallery,
README, design/build docs and open issues/PRs. Existing #82 table host cells,
#89 explicit modal dismissal, #102 modal focus, #106 preloaded styles, #125 field
anatomy, #135 component descriptions and #147/#148 derived/query rows are
references, not duplicated work. Selectable record lists can compose stock
Cards/Stacks instead of inventing table-cell widgets.

The following generic upstream extensions are tracked separately:

| Extension | Upstream tracking | Focused evidence | Remaining gates |
| --- | --- | --- | --- |
| Disabled TextField/TextArea contract | [#154](https://github.com/inferal-oss/semblance/issues/154) | 11 tests pass on the pinned base and 11 on rebased upstream source | Upstream review and full suite |
| Bounded linear Sparkline, signed/zero observations and gaps | [#155](https://github.com/inferal-oss/semblance/issues/155), `93aca30` | 17 tests, WAT preparation, multi-theme screen/text/event parity | Upstream review and full suite |
| Strict-CSP CSSOM styles | [#157](https://github.com/inferal-oss/semblance/issues/157) | 16 affected style/DOM assertions pass; browser checks under strict CSP | Upstream review and full suite |
| Separate palette search metadata | [#166](https://github.com/inferal-oss/semblance/issues/166) | 34 focused palette tests and browser keyword/update/removal/disable checks pass | Upstream review and full suite |
| Exact-number, bounded JsonTree | [#158](https://github.com/inferal-oss/semblance/issues/158) | Not adopted; 40 unique targeted cases pass on the candidate; direct-tree depth is capped at 32 | Independent review, rebase and full suite |

The provenance manifest distinguishes the pinned base from provisional overlays.
Queued tests are not acceptance. No upstream branch or PR has been pushed or
merged. ARM retains exact bounded raw Code disclosures while JsonTree work is
unaccepted.

## Author evidence before independent composition

The composed JavaScript effect-policy suite passes 304 tests, including quantity,
identity, ownership and palette checks. Handle doubles do not establish real
RDF rendering acceptance. Focused HTTP/WebSocket login, bootstrap, screen,
embedded-asset, MIME, HEAD, method, authority, CSP and license tests pass. An
independent locked npm installation reproduces all six embedded artifacts for
the matching source snapshot; subsequent source changes require a new comparison.

Native browser review covers desktop and 390px layouts, light/dark Semblance,
centered MessageList bodies, grouped Host capabilities, compact monitor Stats,
filled CPU bars, linear plots, and Timeline in a side Drawer. All eight views
have zero SHACL violations and no horizontal overflow in desktop and 390px audits.
Compact Picker changes the route and view. Complete-ID message selection focuses
and centers the same retained message on repeated visits. Escape from a
pointer-opened Drawer immediately restores its invoking button. Ineligible
native controls retain normal Tab behavior. Navigation between different views
targets the new heading and starts at the top; live refreshes preserve reading
position. Direct lease URLs survive fresh authentication, focus the modal panel
at its title, and clear the lease only on an explicit close. Dialog dismissal
falls back to visible navigation or the page heading if its opener is hidden.

A read-only four-node cluster inventory renders human-readable CPU, RAM and disk
figures, marks a tainted node unavailable, and preserves exact technical data in
bounded disclosures. Palette selection focuses the matching node action without
selecting a node or provisioning a Pod. This is inventory evidence, not cluster
provisioning acceptance. The current inventory transport requires direct
credentials; external exec credential helpers remain unsupported.

Backend one-use login links are committed as `0ce8b96`; inventory cards and exact
quantity formatting as `6eaa46b`. Source review fixed node-window host guards,
clipboard session races, effect/close reentrancy, native Tab order, and pending
focus restoration after stock events. Real browser clipboard writing reported
success; cross-API clipboard contents were not independently established. HTTP
exchange, single-use, expiry, scope and cap tests cover issued links separately.

The final real RDF/DOM suite passes all 17 tests on identical pre/post source
hashes. It covers stock default/theme rendering, input/choice/checkbox state,
rich MessageList bodies, palette filtering and exact-once effects, exact review
strings and leading zeroes, collection ownership, row retirement, overlay close
notifications and native-control/reading-focus semantics. Earlier findings in
event namespace constants, accessibility rule registration and fixtures are
corrected. Broader mesh, lease, pool, recovery and actual native-screen workflows
remain separate acceptance gates.

The author snapshot retained its earlier Go module namespace. The independent
projection canonicalizes only the owned new Go test import, rechecks build,
embedded assets and server boundaries, and preserves the accepted noVNC pako
relocation outside directories excluded from Go module ZIPs.

## Rebuild

From `web/`, install the exact lock with `npm ci`, then `npm run build`.
Tests use the shared queue in the foreground with its default limits and at most
two workers; set `SEMBLANCE_TEST_QUEUE` to its absolute script before `npm test`.
Never run dependency installation against a node_modules symlink into another
checkout. Temporary dependencies and private execution receipts are not shipped.

## Additional evidence from the frozen author snapshot

The following reports apply to `dbfa5d6ae48da454e3577b9ee8b26c2ddc97569d`;
current independent review gates are not inferred from them.

Six lifecycle checks execute production connection, route, authentication-reset
and lease-detail functions with IO/component boundaries doubled. They cover
reconnection callback ownership, preserved direct links, explicit close, modal
focus order, superseded connections and failed reads without mutations. These
are policy checks, not rendered automatic-reconnect acceptance. A separate
message regression drains held snapshots after focus leaves, even without a
focusout notification.

A native pool-review check found stock automatic formatting rounding the display
of `9007199254740993`, while the prepared request retained that exact integer.
Review Table columns now explicitly use code format. DescriptionList values use
labelled resources through the stock of/fields contract, retaining numeric-looking
identifiers and leading zeroes. The existing RDF exact-value regression also
covers updates, row retirement and disposed-helper no-op behavior; the final
17-test RDF suite passes on the same source. No pool change was applied during that check.

Pool empty inventory is now row content, separate from notices/errors, so
cancelling a create review preserves it. A transition test checks cancellation
without dispatch or rebuilding the row. The composed policy suite passes 304
Node tests; broader native mutation and recovery workflows remain separate.

Final native checks confirm exact pool review of `9007199254740993`, cancellation
without applying that proposal, retained empty inventory, read-only four-node
inventory, and browser Back/Forward restoration. The compiled CLI bootstrap,
origin/authority/session-replay/PTY harness and final focused Go asset/auth/screen
checks pass. Six embedded artifacts match an independent locked installation
byte for byte. Source/asset review used an isolated snapshot and found no
remaining concrete findings within this scope.
