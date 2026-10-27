# Proposed getarm.org source installation

Status: planning only, awaiting coordinator review before source implementation.
Inventory base: `6509f5f10fe8589face1d067057a9f6a476f3c8b`, tree
`f92c94a5545db7e43ead0795a2d6e52cc0c98649`. Later coordinator documentation
commits are not a reason to change this inventory cut. A reviewed VM clock fix
must be composed before the eventual namespace implementation/source gate.

## Public contract

```sh
go install getarm.org/cmd/arm@latest
arm version
```

The canonical Go module becomes `getarm.org`; its executable package remains
`cmd/arm`, so the installed filename stays `arm`. Do not add a root `main`
package: `go install getarm.org@latest` would select the wrong binary basename.
Ordinary source installation remains thin, with an empty companion catalog.
It does not download a release/carrier, install a backend, or start one.

The Git repository, origin and release identity remain:

| Identity | Value |
| --- | --- |
| Go module | `getarm.org` |
| Executable package | `getarm.org/cmd/arm` |
| Executable filename / CLI brand | `arm` / ARM |
| Repository URL / full Go Origin URL | `https://github.com/inferal-oss/arm` |
| Repository API/release identity | `inferal-oss/arm` |
| Metadata mapping | `getarm.org git https://github.com/inferal-oss/arm` |

This changes the current canonical module. The old GitHub-path `@latest`
command will no longer be a supported latest-install alias after publication:
its requested module path and the new go.mod declaration disagree. Existing
immutable historical GitHub-path versions and their evidence remain historical
facts. No compatibility shim or dual-module tree is proposed. The Git clone URL
and `go install ./cmd/arm` from an appropriate checkout remain valid.

## Measured inventory and edit policy

The base has 953 tracked UTF-8 files containing the old module text, with 3,183
occurrences. This is an inventory, not a global replacement list.

| Area | Measured impact / proposed treatment |
| --- | --- |
| Go source | 901 files reference the old namespace; 898 contain 2,079 import lines. Rewrite owned Go import prefixes, including the in-module rsync fork. Three files contain namespace literals without self-imports. |
| Module declaration | Change only go.mod's module line initially. No dependency upgrade, local replacement, nested go.mod, package relocation or root command is needed. go.sum is not an import-prefix rewrite target. |
| Production build identity | Four non-import literals: distribution linker package and executable path, plus mesh artifact and companion-stream executable paths. Handle these explicitly. |
| Go fixtures | Three additional literal-bearing lines in carrier_target_test.go and module_layout_test.go, including a generated tiny module/main. Update their declared module, imports and expected identity together. |
| Active Python drivers | Eight files have current module/package identifiers. Six concern actual Go Package trace matching; validate-module.py and module-zip-supplement.py concern module/provenance/ZIP identity. |
| Additional validation work | test-validate-module.py uses MODULE indirectly and needs repository-identity fixtures even though it has no literal old module. Current workflow hash closures must be reviewed anew after validator changes. |
| Fork developer files | Two current install commands in third_party/rsyncgo/Makefile and current ARM-owned package-path prose in ARM-CHANGES.md. Preserve upstream licenses, original renamed module metadata and upstream repository provenance. |
| Current documentation | Add vanity source installation to README while retaining GitHub clone instructions. Supply current-module/validation updates for root's central PLAN/IMPLEMENTATION/VALIDATION ownership. Preserve historical sections. |
| Historical evidence | All 30 matching docs/validation JSON files retain original paths, hashes, source SHAs and URLs. Preserve recorded actual Package events and both failed and successful historical runs. |
| Repository references | Preserve README clone/source links, release API fixtures, website repository/issues/license links, notices, historical CI links and the archived design patch. |
| Website | Four existing website files contain repository references, which stay GitHub references. Website owner adds metadata pages and changes only installation copy; this actor does not edit/deploy the website or Cloudflare. |

The production literals needing review are:

- `internal/distribution/bundle.go`: `identityPackage` becomes
  `getarm.org/internal/buildinfo`; inspect's executable path becomes
  `getarm.org/cmd/arm`. Otherwise version/BundleTag injection or inspection can
  fail despite imports compiling.
- `internal/mesh/artifacts.go`: the fresh artifact inspector expects the new
  executable path.
- `internal/mesh/artifact_stream.go`: new companion stream inspection expects
  the new executable path.

Active trace drivers needing new Package identities are:

- scripts/export-lease-review-trace.py
- scripts/test-cpu-admission-formal.py
- scripts/test-cpu-warning-formal.py
- scripts/test-history-cleanup-formal.py
- scripts/test-node-stop-formal.py
- scripts/test-uninstall-formal.py

The upstream rsync license and UPSTREAM-go.mod.txt / UPSTREAM-go.sum.txt remain
byte-for-byte preserved. Only ARM-owned imports/current packaging prose change.
Do not rewrite original repository URLs to getarm.org.

## Validators: module identity is not repository identity

Proposed explicit constants in the current validators:

```python
MODULE = "getarm.org"
REPOSITORY_URL = "https://github.com/inferal-oss/arm"
```

Path, literal install/query argv, installed buildinfo and ZIP entry prefixes use
MODULE. Selected provenance uses VCS `git`, the unchanged REPOSITORY_URL and the
full expected checkout commit. Preserve all existing clean-source/harness,
selected-Version/Sum/GoModSum, checksumdb, no-replace, normal-CGo, smoke/empty
catalog and cleanup guards. Add explicit wrong/missing repository URL fixtures;
do not derive repository identity from MODULE or weaken full-Origin matching.
Go may omit query-level Origin; preserve the fresh lookup of the version actually
selected and the equality checks. It never substitutes an expected version.

The full ZIP Go h1 changes with the new module prefix and source. GoModSum also
changes with the go.mod declaration. Use actual new query/selected-version/binary
values; old GitHub-module checksums are not acceptance values for the vanity
module. The public proxy ZIP URL becomes the actual selected
`https://proxy.golang.org/getarm.org/@v/VERSION.zip` only after publication gates.

Existing frozen helper hashes describe old bytes. After new MODULE/REPOSITORY
checks and tests are reviewed, freeze their new hashes, update the supplement's
helper closure and prepare a separate reviewed vanity CI topic/workflow. Do not
make the old source-bound workflow appear to validate the new module using its
old helper hashes or source pin. Historical records and original failed handles
are unchanged. Keep sibling checkouts, full ZIP h1, complete-root manifest,
64 MiB compressed bound, streaming SHA, special-file refusal, no automatic
retries and seven-day raw ZIP/original receipt retention.

## Website metadata handoff

Insert this early in the HTML head, after charset and before scripts/styles:

```html
<meta name="go-import" content="getarm.org git https://github.com/inferal-oss/arm">
```

Recommended final-response contract:

| Request | Response |
| --- | --- |
| `https://getarm.org/?go-get=1` | Authoritative HTTPS 200 HTML head with the exact tag. |
| `https://getarm.org/cmd?go-get=1` | HTTPS 200 HTML head with the same tag; proposed extra candidate-prefix coverage. |
| `https://getarm.org/cmd/arm?go-get=1` | HTTPS 200 HTML head with the same tag. |

The apex proves the non-authoritative subpath mapping. No `cmd/arm` subdirectory
field is used because go.mod stays at the Git repository root. No competing
`mod` mapping, different-prefix repository or release-download redirect is used.
The HTML must be server-delivered: a JavaScript-rendered meta tag is insufficient.
The project contract requires final status 200 with an HTML content type and
valid public TLS. A same-origin HTTPS trailing-slash redirect is acceptable only
if the query survives and the final page meets the same contract. Record its
chain. Do not accept a 404 SPA fallback, bot challenge or authentication page.

Go's implementation can parse metadata from some non-200 responses; requiring
200 here is the deployment acceptance contract, not a claim that every Go parser
rejects every error-status page. Go's package resolver considers plausible module
prefixes, which motivates the additional `/cmd` page.

Website owner can place plain files at `website/public/cmd/index.html` and
`website/public/cmd/arm/index.html` for Vite's static public copy, and put the
same tag in `website/index.html`. Verify the actual built Pages output and URLs;
do not assume that unbuilt source, slash redirects or a custom 404 supply them.
Root receives an illustrative private hunk; this plan applies no website edits.

## Carrier, protocol, schema and display boundaries

- Newly built payloads/carriers carry the new Go package identity and linker
  symbols. Test version and BundleTag injection through native and cross-target
  build boundaries. Preserve four target payloads, CGo-free carrier rules,
  catalog integrity checks, bounded import and actual platform metadata checks.
- Preserve `LifecycleProtocol = 1`, artifact/catalog schema 1 and
  `arm-bundle-v1` unless a separate behavior change warrants a separate review.
  This namespace change alone does not justify a wire or database migration.
- No Go gob registration or reflect.PkgPath-based wire identity was found in
  current cmd/internal/scripts Go source. Current wire/schema JSON does not use
  the canonical module string. Hold JSON tags, protocol keys, resource/lease
  semantics, ARM_HOME, service/state paths, command names and display branding.
- Compatibility decision: strict new inspectors will reject freshly imported
  old GitHub-path binaries. Existing trusted catalog entries are reopened through
  descriptor/digest/stat checks without repeating the module-path inspector.
  Recommended namespace-only policy: keep that existing trusted-catalog behavior,
  require the vanity path for new imports, document the distinction, and never
  silently delete/rewrite catalogs or add an unreviewed dual-identity allowlist.
  Root must review this policy before implementation. Existing authenticated node
  version/protocol reuse remains separate from artifact import and does not imply
  automatic upgrades.

## Ownership and composition order

| Owner | Bounded responsibility |
| --- | --- |
| Root coordinator | Approve plan/compatibility policy, integrate reviewed VM fix, own final composition and central documentation, authorize publication and one final public gate. |
| Module actor, this existing chat | Canonical module/import/fork namespace, four production identity literals, fixtures/trace matchers, validator MODULE/REPOSITORY split and focused native/source-install evidence. |
| VM/sleep-wake actor | Existing authorized guest S3 experiment and any focused reviewed clock fix from its 6509 base. No race with a mass namespace edit. |
| Website actor | Gallery/alignment, metadata head/static pages, installation copy and actual Pages HTTP checks. No source actor hosting or Cloudflare edits. |
| Root/release owner | Keep GitHub repository/release identity and explicit release authorization boundaries; no tag/release creation in this migration. |

1. Review this plan first. Inventory preparation does not authorize applying the
   namespace patch or running a build.
2. Root supplies the focused reviewed VM clock commit, if any. Compose it into
   the namespace implementation base first; do not import unreviewed VM history
   or run a source gate on an obsolete clock cut. Regenerate inventory if needed.
3. Apply the namespace-only change in isolation. Keep carrier compatibility and
   validator changes explicit, independently review the actual final diff, then
   run approved bounded local/native checks. No public getarm.org Go query yet.
4. Compose reviewed website metadata and namespace/VM source into one publication
   cut. Root freezes immutable source S and all new helper hashes. This avoids a
   later website/evidence commit accidentally changing expected latest Origin.
5. Root publishes canonical source S and website deployment; hold new install
   advertising/proxy queries until canonical go.mod and all metadata pages are
   actually live. Check HTTPS metadata directly, without invoking Go/new proxy.
6. Only after both are ready, separately authorize ordinary vanity latest
   observation and one source-bound native Linux/macOS gate at S. Hold main
   publication during the gate. Never substitute a computed expected pseudo-version,
   use a fake proxy/direct fallback, or auto-retry a mismatched selection.
7. Preserve actual selected source, all original results and raw artifacts.
   Independently review the result before describing vanity installation as
   accepted. Old source23 acceptance remains scoped to its GitHub module identity.

## Acceptance cases after implementation review

1. Static: canonical go.mod is getarm.org; all active self/fork Go imports and
   current package-match drivers use it; no replacements/nested modules. Repository
   URLs and historical validation files remain unchanged, with an explicit residual
   old-string allowlist rather than a blind zero-occurrence requirement.
2. Dependencies: inspect go.mod/go.sum changes; no unrequested dependency upgrade.
   Preserve audited fork license/upstream metadata and ZIP-visible pako layout.
3. Local builds: approved shared bounded queue, GOMAXPROCS=1 and Go -p=1; begin
   with all-package/test compilation and focused distribution/mesh artifact tests.
   Use pinned cached dependencies with GOPROXY=off before metadata publication;
   a cache miss is a reported gate, not permission to query the new public module.
4. Carrier: new linker package injects expected version and BundleTag; actual
   native/cross metadata uses getarm.org/cmd/arm. Wrong path/platform/CGo/version
   and malformed/untrusted bytes remain refused. Exercise the approved old
   trusted-catalog versus fresh-import policy without changing real user state.
5. Wire/schema: review unchanged protocol/schema/constants and JSON shape;
   namespace-only tests must not become a version/protocol bump or data migration.
6. Validators: accept vanity MODULE plus GitHub REPOSITORY and exact full hash;
   reject wrong/missing repo, hash, Path, Version, Sum, GoModSum or buildinfo.
   Preserve original literal latest argv, no-replace and empty catalog tests.
7. Website: static/built HTML and deployed HTTPS endpoints have matching early
   meta tags, final 200, HTML content type, valid TLS and no JS dependency.
   Reject metadata only in body/script, conflicting mappings, wrong repository,
   error-status fallback or release redirect. Confirm the `/cmd` candidate page.
8. Final public native gate: actual literal `go install getarm.org/cmd/arm@latest`
   installs a binary named arm in a fresh private GOBIN outside both clean
   checkouts. Its actual version/sums/Origin repository/full hash match source S.
   Normal CGo and public HTTPS proxy/checksumdb remain unchanged. Compare the full
   ZIP h1 and regenerated complete-root inputs; retain raw ZIP and receipts.
9. Smoke: version/help and an empty companion catalog succeed with a private
   fresh home and no backend/user-install side effects. Owned temporary state is
   removed; interrupted descendant cleanup remains explicitly unproven.
10. History/coordination: preserve all original failed runs, source23 success,
    exact actual selections and native handles. No tags/releases, user installs,
    new pipelines, hosting changes or publication until separately coordinated.

## Decisions requested for root review

- Accept the namespace-only artifact policy: new imports require the vanity path,
  existing already-trusted catalog behavior stays unchanged, with no silent
  schema/protocol bump or dual-path exception.
- Accept the three-URL metadata contract, including the small additional `/cmd`
  page, and assign its exact hunk to the website actor.
- Identify the reviewed VM clock fix/composed base and approve namespace source
  implementation and its bounded validation scope before any source edits/builds.

Inventory/protocol findings were checked against repository source and installed
Go 1.27.3 source (`cmd/go/alldocs.go`, `internal/vcs/discovery.go`,
`internal/vcs/vcs.go`, `internal/modload/query.go`). No Go process, external
network/proxy request, website edit, build, install, CI dispatch or publication
was used to prepare this proposal.
