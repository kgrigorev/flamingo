# CUE v0.17 migration for Flamingo v4

Status: proposal

**Decision summary**

- Flamingo v4 moves `cuelang.org/go` from v0.0.15 to v0.17.1 in one step. There is no intermediate version to stop
  at and no compatibility mode.
- The safety net ships first, in **v3.N** on CUE v0.0.15: a `.cue` file that fails to load stops the boot instead of
  being silently ignored, and `config snapshot` records every config key with its Go type and a hashed value.
- **v4.0.0** ports the Go API and adds guards that reproduce v3 results: a package clause on generated files and
  module schemas, decoding through JSON (numbers stay `float64`), and a null-override check. It is accepted only if
  it reproduces the v3.N golden snapshots exactly.
- Users convert their `.cue` files with `config cue-migrate`, which ships in **v3.M**. They then compare a v3 snapshot
  with a v4 snapshot for each deployed context. Rollback is a revert of one commit.

These placeholders are used here and in the [user guide](cue-migration-guide.md):

| Placeholder | Meaning |
| --- | --- |
| v3.N | v3 minor release with strict `.cue` loading, `config snapshot` and dual-valid framework schemas (Phase 1) |
| v3.M | later v3 minor release that adds `config cue-migrate`; contains everything in v3.N (Phase 3) |
| v4.0.0-rc.1 | first v4 release candidate (Phase 2) |
| v4.0.0 | v4 general availability (Phase 3) |

## Summary

Porting the Go code is small. The risk is configuration that keeps loading but means something different.

- The port touches five call sites, all in `framework/config`: `go build` reports three, and the test files add two.
- 13 of the 28 module schemas fail to parse or build on v0.17.1. Flamingo has 21 of the 28 and flamingo-commerce 7.
  All schemas of an area are built as one instance, so one failing schema stops the config load of every app that
  includes that module. YAML-only apps are included.
- Most removed syntax has a **dual-valid** spelling: v0.0.15 and v0.17.1 both accept it with identical results. That
  syntax can be migrated on v3. Definitions (`Foo ::` → `#Foo:`) and a few rare constructs have no dual-valid
  spelling and change at the cutover.
- Four behaviour changes would be silent without a guard, and each gets one: dropped `.cue` files, a null overriding
  a schema default, non-concrete top-level values decoding to nil, and integer types inside lists. Every other
  difference fails loudly at boot.

## Scope and non-goals

**In scope:** the `framework/config` port and its guards, the framework and flamingo-commerce schema migration,
`config snapshot` and `config cue-migrate`, golden snapshots and CI probes, and the user guide.

**Non-goals:**

- **Changing config semantics beyond reproducing v3.** Numbers stay `float64`.
- **Removing the deprecated config API** (`DefaultConfigModule`, `OverrideConfigModule`, `LoadConfigFile`,
  `GetFlatContexts`). The two module interfaces are detected by duck typing, so removing them would silently drop
  behaviour in modules that implement them. That needs its own proposal.
- **Replacing the `.cue` merge in `cueast.go`.**
- **Fixing the version-neutral hazards** listed under [What this plan cannot catch](#what-this-plan-cannot-catch).
- **Intermediate CUE versions, a two-version predictive tool, or a reverse converter.**

## Current state

- **Versions.** Flamingo pins `cuelang.org/go v0.0.15` (2019-12-05). The latest stable release is v0.17.1 (2026-07-16).
  A 2021 attempt to move to v0.4.0 (upstream branch `201-update-cue`, commit `d616ac4`) fixed label paths in four
  modules and then stalled.
- **Where CUE is used.** CUE is imported only in `framework/config`: `area.go`, `loader.go`, `cueast.go`,
  `configcmd.go` and `cueast_test.go`. flamingo-commerce has no direct CUE import and no `.cue` files. It contributes
  schema strings from 7 modules.
- **The pipeline** in `Area.loadConfig`:
  1. `Area.loadCueConfig` adds anonymous files: the `flamingo.modules.disabled` schema, every module's `CueConfig()`
     (named `<import path>.<Type>`), and a synthesized `flamingo.os.env` file.
  2. Go defaults and YAML are merged in Go by `config.Map.Add`, which turns scalar numbers into `float64`.
     `loadYamlConfig` substitutes `%%ENV:X%%` in the raw YAML bytes. If X is unset or empty, `'%%ENV:X%%'` becomes
     `""` and unquoted `%%ENV:X%%` becomes YAML null.
  3. `checkLegacyConfig` maps `FlamingoLegacyConfigAlias` keys. Its three `log.Fatal`/`log.Fatalf` calls stop the
     process on an error or a mismatch.
  4. Every nil leaf is marked `*null | _` (purgeNil). This is still required on v0.17.1.
  5. `cueAstMergeFile` merges the user `.cue` files into one AST, where the last file wins for scalars, and adds it.
  6. The instance is built, filled with the merged map and decoded, and the result is merged back.
  7. `Area.GetInitializedInjector` binds every key into Dingo by Go type. Integral `float64` values are also bound as
     `int` and `int64`.
- **Hidden failures.** `loadLogged` discards every `.cue` load error, parse errors included, unless
  `--flamingo-config-log` is set. `loadCueFile` treats any open error as "no file". A `CONTEXTFILE` entry that
  matches no file is ignored. `Area.Flat()` discards the load errors of child areas.
- **Coverage.** 15 test files use `config.TryModules`, which runs build, fill and decode. 5 use `dingo.TryModule`,
  which never evaluates `CueConfig()`. 9 of the 20 packages that define a `CueConfig()` have no `config.TryModules`
  test: the root package (`servemodule`), `framework`, `framework/cmd`, `framework/prefixrouter`,
  `framework/systemendpoint`, `core/auth/fake`, `core/auth/http`, `core/auth/example/custom` and `core/silentzap`.

## Target state

v4.0.0 runs on CUE v0.17.1. For every configuration that loads on v3.N, it yields the same keys, values and Go types,
or it fails at boot with a positioned error. Four checks enforce this continuously: golden snapshots, a
null-override parity table, a per-module schema probe, and a scheduled job against the latest CUE release. The
scheduled job replaces today's manual freeze on CUE upgrades.

## What changes

### Go API port

| Function | v0.0.15 | v0.17.1 |
| --- | --- | --- |
| `Area.loadConfig` | `new(cue.Runtime).Build(bi)` | `cuecontext.New().BuildInstance(bi)`, then check `.Err()` |
| `Area.loadConfig` | `inst.Fill(m)` | `v.FillPath(cue.Path{}, m)`, then check `.Err()` |
| `Area.loadConfig` | `inst.Value().Decode(&m)` | the decode in [Semantic changes](#semantic-changes-and-guards) |
| `Load` (`CueDebug` option) | `inst.Lookup(path...)` | `v.LookupPath(...)`; `Syntax()` needs `cue.Final()` |
| `cueast_test.go` (2 sites) | `new(cue.Runtime).Build` | as for `Area.loadConfig` |

- **Unchanged APIs.** `build.Instance`, `parser`, `format`, `errors` and the `ast` types used by `cueast.go` stay as
  they are.
- **Nil values.** Together with purgeNil's marker, `FillPath` handles Go nil values the way v3's `Fill` did.
  `v.Unify(ctx.Encode(m))` does so only with `cue.NilIsAny(true)`. Without it, a nil under a typed field fails with
  `2 errors in empty disjunction`.
- **Build setup.** No `cue.mod` is needed, because Flamingo never calls `load.Instances`. CUE v0.17.1 needs Go 1.25.0,
  and Flamingo declares Go 1.25.8.

### Syntax

v0.17.1 has no language-compatibility switch. `parser.Version("v0.0.15")` still rejects all the removed syntax, and
no older evaluator can be selected.

**Dual-valid rewrites** can be applied on v3. Both versions give identical results.

| v3-only form | Dual-valid form | Removed in | Converted by |
| --- | --- | --- | --- |
| `core zap: {…}` (space-separated labels) | `core: zap: {…}` | v0.2.0 | `cue fmt` v0.0.15 |
| `<Name>: {…}` template | `[Name=string]: {…}` (fmt writes `[Name=_]`) | v0.2.0 | `cue fmt` v0.0.15 |
| `/* … */` | `// …` (moved to the line before) | v0.1.0 | `cue fmt` v0.0.15 |
| `{"\(k)": v for k, v in s}`, `{y: 1 if c}` | `{for k, v in s {"\(k)": v}}`, `{if c {y: 1}}` | v0.2.0 | `cue fmt` v0.0.15 |
| `if` on an unset optional field (schemas) | a default, or a disjunction as in prefixrouter below | v0.1.0 | by hand |

**Version-locked constructs** have no practical dual-valid form, so they are converted at the cutover.

| v3 form | v4 form | v0.17.1 error | Removed in | Converted by |
| --- | --- | --- | --- | --- |
| `Foo :: {…}`, references `Foo`, `a.Foo` | `#Foo: {…}`, `#Foo`, `a.#Foo` | `expected operand, found ':'` | semantics v0.3.0, parser v0.5.0 | `config cue-migrate` |
| `[x*2 for x in y]` | `[for x in y {x*2}]` | `expected ']', found 'for'` | evaluation v0.3.0, parser v0.4.0 | `config cue-migrate` |
| `X = 3` alias | `let X = 3` | `expected label or ':', found 'IDENT' …` | evaluation v0.4.0, parser v0.5.0 | `config cue-migrate` |
| `a div b` (`mod`, `quo`, `rem`) | `div(a, b)` | `missing ',' in struct literal` | v0.17.0 | `config cue-migrate` |
| `[1] + [2]`, `2 * [1]` | `list.Concat(…)`, `list.Repeat(…)` | `Addition of lists is superseded by list.Concat` | v0.11.0 | `cue fix` v0.17.1 (literal operands) |

- **No dual-valid definitions.** No dual-valid spelling of a definition keeps its reference path. A hidden field with
  `close()` (`_http`) is valid on both versions, but it renames the path and loses recursive closedness (decision 9).
- **v4-only syntax on v3.** v0.0.15 cannot parse `#Foo`, `[for …]`, `let`, `a!:`, declaration attributes, or the
  `div()` and `list.Concat`/`Repeat`/`Sort` builtins. Before v3.N, v3 **silently drops** a file that contains them.
- **No useful intermediate version.** Only v0.2.0 to v0.2.2 evaluate both `::` and `#` with v0.0.15 semantics. Those
  releases already reject space-separated labels, templates and block comments, and their `cue fix` is defective
  (decision 10).

**Same text, different result.** All of these are loud on v4 except the last one:

- **Closedness inside `if` blocks.** A definition or `close()` value introduced inside `if` is enforced on v0.17.1 but
  not on v0.0.15, whatever the spelling. For example, YAML typos under
  `commerce.checkout.placeorder.contextstore.redis` or `commerce.product.fakeservice.sorting[*]` boot on v3 and fail
  on v4 with `field not allowed`.
- **Wrong-typed `if` guards.** A field used as an `if` guard with a value of the wrong type (`enabled: "yes"`) is
  silently kept by v3 and rejected by v4.
- **Nested `close()` merged through embedding.** v4 rejects it with `field not allowed`.
- **Package clauses in user files.** A user `.cue` file with `package flamingo` fails on v3 and loads on v4. Any
  other package name fails on both.
- **Silent: lost validation.** `v: #A; z: v.c & {zz: 1}` is rejected by v0.0.15 and accepted by v0.17.1. See
  [What this plan cannot catch](#what-this-plan-cannot-catch).

### Semantic changes and guards

The v4 decode does not use native `Value.Decode`. It runs `Validate(cue.Concrete(true))`, formatting any error with
`errors.Details`, then `MarshalJSON`, then `json.Unmarshal` into `config.Map`.

| Change | Trigger | v3 | v4 without guard | Guard | Residual risk |
| --- | --- | --- | --- | --- | --- |
| Unloadable `.cue` file | Parse error (legacy syntax on v4, v4 syntax on v3, typos), unreadable file, or a `CONTEXTFILE` entry with no file | Silently skipped; the app boots without that file's values | Same | Strict loading: v3.N (with an opt-out) and v4.0.0 (without) | v3 users who set `--flamingo-config-lenient`; each skipped file is logged |
| Null over a schema default | YAML null on a key whose schema has a default: unquoted `%%ENV:X%%` with X unset or empty, `~`, or `null` | Boot error for scalar and struct defaults; `[]` for a non-empty list default | The default is used silently: 35 defaulted keys without a legacy alias in flamingo (e.g. `flamingo.router.notfound`, `core.serve.port`), 40 in flamingo-commerce (e.g. `commerce.pagination.defaultPageSize`), and list defaults (`commerce.product.fakeservice.deliveryCodes: ~`). Keys with a legacy alias, such as `flamingo.session.secret`, fail in `checkLegacyConfig` instead | Null-override check (below) | None for any schema shape in flamingo or flamingo-commerce. The hypothetical `{a: string \| *"x"} \| *{a: "y"}` fails where v3 booted, which is loud |
| Non-concrete top-level value | A top-level user `.cue` field that is not concrete: an unmigrated reference `H: core.auth.http & {…}`, `T: string`, or conflicting top-level defaults | Load error | Decodes to nil, and `Get` reports the key as present | Validate plus JSON decode fail with the position | None observed |
| Integer Go types | Numbers from CUE inside lists (literals, schema defaults) | `float64` | `int64` inside `config.Slice`; scalars are re-normalised by `Map.Add` | JSON decode | None observed |

**Null-override check.** It runs after `FillPath`, for each key whose merged Go value is nil. That is the same set
that purgeNil marks.

| Schema at the key | Action | Compared with v3 |
| --- | --- | --- |
| none, no default, or default `null` | keep null | already equal |
| open list with only an implicit default (`[...T]`, `[x, ...T]`) | keep | already equal |
| list with an explicit default, e.g. `[...T] \| *[a, b]` | reset to the shortest prefix of the default that the schema accepts (here `[]`); if only the full non-empty default is accepted, fail | v3 result |
| any other default (scalar, enum, reference, struct) | fail with `ErrNullOverridesDefault` | v3 failed too |

The error reads: `config key "flamingo.router.notfound" is null (an unset or empty unquoted %%ENV:X%%, or ~/null)
but its schema default is "flamingo.notfound"; v3 refused this config: set a value, use %%ENV:X%%default%%, or
remove the key to accept the default`.

Unification cannot express this rule. With a v3-style marker `*null | bool | number | string | bytes | [...] | {...}`,
v0.17.1 still resolves to the default.

The legacy-alias check also acts as a guard. Today it is the only thing that stops `flamingo.session.secret: ~` from
booting on the schema default `"flamingosecret"` on v4. Its `log.Fatal` calls therefore become returned errors (work
item 1.4), and never warnings.

### Framework and ecosystem schemas

| Cause on v0.17.1 | Schemas | Detected at | Fix | Ships in |
| --- | --- | --- | --- | --- |
| space-separated labels | `core/silentzap`, `core/gotemplate`, `core/oauth` | parse | colon label paths | v3.N (dual-valid) |
| `if` on the unset optional `enabled` | `framework/prefixrouter` | build: `cannot reference optional field: enabled` | `flamingo: prefixrouter: rootRedirectHandler: *{enabled?: false} \| {enabled: true, redirectTarget: string}` | v3.N (dual-valid) |
| cross-file reference, no package clause | `core/auth` (`WebModule` reads `flamingo.debug.mode`) | build: `reference "flamingo" not found` | none in the schema; v4 adds the package clause | v4.0.0-rc.1 |
| `::` definitions | `core/auth/http`, `core/auth/fake`, `core/auth/oauth`, `core/auth/example/custom` (2) | parse | `#` definitions and references | v4.0.0-rc.1 |
| `::` definitions | flamingo-commerce `category`, `checkout`, `product` | parse | `#` definitions and references | commerce v4-compatible release |

- **Totals.** 13 of 28 schemas fail: 10 of 21 in flamingo and 3 of 7 in flamingo-commerce.
- **prefixrouter rewrite.** It is identical for every valid input and adds no keys. Only a wrong-typed `enabled` now
  fails.
- **`fmt.Sprintf` schemas.** Four schemas are built with `fmt.Sprintf` (`core/zap`, `core/silentzap`,
  `core/security`, `framework/cmd`). Only silentzap needs an edit.
- **In-repo `.cue` material.** `core/auth/example/config/config.cue` (six `::` definitions), `config_static.cue`
  (which references them), `core/oauth/example/config/config.cue`, three files under `framework/config/testdata`, the
  `cueast_test.go` fixtures, and one `cue` block in `core/auth/fake/Readme.md`.
- **User-reachable definitions.** Users can reference 11 definition paths: 5 under `core.auth`, 2 in
  `core/auth/example/custom` and 4 in flamingo-commerce. The guide has the rename table.

### Error quality

`cueError` in `area.go` passes only `err.Error()` through. On v0.17.1 that text truncates disjunction errors, which
occur in 24 of the 28 schemas. For example, YAML `flamingo.router.timeout: 5000` fails on both versions, because YAML
numbers arrive as floats:

```
v3: conflicting values (int | *60000) and 5000 (mismatched types int and float)
v4: flamingo.router.timeout: 2 errors in empty disjunction: (and 2 more errors)
```

`errors.Details` does not help on the `MarshalJSON` error, which stays one line. On the error from
`Validate(cue.Concrete(true))` it prints every branch, for example `flamingo.router.timeout: conflicting values 5E+3
and int (mismatched types float and int)`, each with the schema file, line and column. That is why the v4 decode
validates first.

### Cost and dependency surface

Measured with go1.26.5 on linux/amd64:

| Measure | v3 (CUE v0.0.15) | v4 port (CUE v0.17.1) |
| --- | --- | --- |
| `examples/hello-world` binary | 22.83 MB | 28.39 MB (+5.56 MB, +24%) |
| `cuelang.org` packages linked into `framework/config` | 13 | 96 |

- **`go.mod`.** Besides `cuelang.org/go`, it changes `cockroachdb/apd/v2` to `apd/v3` and bumps `golang.org/x/*`.
- **Module graph.** `go mod graph` and `go.sum` gain `tetratelabs/wazero` (a WebAssembly runtime),
  `cuelabs.dev/go/oci/ociregistry`, `opencontainers/image-spec` and `coder/websocket`. None of them is linked into the
  binary, and the release notes say so.
- **Environment.** `cuecontext.New` reads `CUE_EXPERIMENT` and `CUE_DEBUG`.
- **Runtime cost.** Not measured yet (work item 2.8).

## Plan

**Roles:**

| Role | Owns |
| --- | --- |
| Config owner | Maintainer of `framework/config`. Owns the code work items and decides technical choices inside them |
| Release manager | Tags, release notes, the soak, the GA decision and the v3 support window |
| Commerce maintainer | The flamingo-commerce migration and release |
| Docs owner | The user guide and Readme updates |

The phases run in dependency order. No work item depends on a later phase.

### Phase 1: v3.N hardening (v3 minor release, CUE v0.0.15)

| # | Work item | Done when | Owner |
| --- | --- | --- | --- |
| 1.1 | `config snapshot` subcommand. It writes one sorted line per area and leaf key with `area`, `key`, the Go type (recursing into `config.Slice` elements) and the SHA-256 of the canonical JSON value (HMAC with `--salt`). It omits `flamingo.os.env`. An area that fails to load becomes one error line | Unit tests cover each rule; the output is byte-stable | Config owner |
| 1.2 | Baselines: golden snapshots of the reference apps ([Validation](#validation)) and the null-override parity table. Record them after 1.1 and before 1.3 to 1.6 | Committed, and `go test ./...` compares against them | Config owner |
| 1.3 | Strict `.cue` loading. `loadCueFile` returns parse errors (file:line:col) and open errors other than "does not exist". `load` and `loadConfigFromBasedir` return these errors instead of passing them to `loadLogged`. A non-empty `CONTEXTFILE` entry with no `.yml`, `.yaml` or `.cue` file fails with `ErrContextFileMissing`. YAML loading is unchanged. `--flamingo-config-lenient` brings back skipping and logs each skipped file | Tests cover a malformed root, context, local, child-area and `CONTEXTFILE` `.cue` file, and a missing entry. The goldens are unchanged | Config owner |
| 1.4 | Legacy-alias failures become errors. The three fatal calls in `checkLegacyConfig` return errors that wrap `ErrLegacyConfigMismatch`, and `loadConfig` returns them. `Area.Flat()` propagates this class of error from child areas; other child errors stay discarded, as today | The parity table and existing tests pass unchanged, and a mismatch still stops `flamingo.App` with the same text | Config owner |
| 1.5 | Dual-valid framework schemas: colon label paths in silentzap, gotemplate and core/oauth, and the prefixrouter disjunction | The goldens are unchanged | Config owner |
| 1.6 | Schema probes: a `config.TryModules` test for each of the 9 uncovered packages, plus one test that loads all schema modules together | All 21 schema modules are covered | Config owner |
| 1.7 | Release notes: the new boot failures, `--flamingo-config-lenient`, `config snapshot`, the v4 timeline and the v3 support window | Published with v3.N | Release manager |

**Exit criteria:** `CGO_ENABLED=1 go test -race ./...` is green, the goldens and the parity table are committed and
pass, a malformed `.cue` file stops the boot and reports its position, and v3.N is tagged.

### Phase 2: v4.0.0-rc.1 (v4 branch, CUE v0.17.1)

| # | Work item | Done when | Owner |
| --- | --- | --- | --- |
| 2.1 | Module path `flamingo.me/flamingo/v4`; `cuelang.org/go v0.17.1`; `go mod tidy` | Builds together with 2.2 | Config owner |
| 2.2 | Go API port (table above). Fields of type `*cue.Instance` become `cue.Value`. `cueAstMergeDecls` returns an error instead of making an unchecked type assertion. The `cueast_test.go` fixtures move from `::` to `#` | The `cueast` tests pass with unchanged assertions | Config owner |
| 2.3 | Package clause: `package flamingo` goes into the modules-disabled, env and purgeNil files, and into every module schema that has none. User files are left alone | `core/auth` builds with its schema unchanged, and a user `.cue` file that reads `flamingo.os.env.X` loads | Config owner |
| 2.4 | Decode: `Validate(cue.Concrete(true))` with `errors.Details`, then `MarshalJSON` and `json.Unmarshal` | Tests cover non-concrete top-level values and CUE-sourced numeric lists. Errors show every disjunction branch with its position | Config owner |
| 2.5 | Null-override check (rule above) | The parity table from 1.2 passes unchanged | Config owner |
| 2.6 | Strict loading without an opt-out: `--flamingo-config-lenient` is removed | The tests from 1.3 pass | Config owner |
| 2.7 | Hand-migrate the framework `::` material: 5 schemas, 3 example `.cue` files and the `core/auth/fake/Readme.md` block. Update `framework/config/Readme.md` | The goldens are identical to Phase 1 with an empty allowlist, the `-race` tests are green, and all 21 probes are green | Config owner |
| 2.8 | Boot-cost benchmark: `config.Load` for hello-world and for an app with many child areas, on v3.N and on the release candidate | The benchmark is committed and its result is in the release notes | Config owner |
| 2.9 | flamingo-commerce on the release candidate: `::` → `#` in `category`, `checkout` and `product` | Commerce unit and integration tests are green on rc.1, and its integration project's snapshot equals its v3.N baseline | Commerce maintainer |
| 2.10 | Tag v4.0.0-rc.1. The release notes cover the dependency surface, the CUE environment variables, error-text changes and the loud changes | Tagged | Release manager |

**Exit criteria:** rc.1 is tagged. Every golden equals its Phase 1 version or has an allowlist entry that the release
notes justify (the expected allowlist is empty). The parity table and the probes are green, and flamingo-commerce is
green on rc.1.

### Phase 3: v3.M tooling, soak and v4.0.0

| # | Work item | Done when | Owner |
| --- | --- | --- | --- |
| 3.1 | `config cue-migrate` in v3.M (described below) | For every in-repo `.cue` file and the 8 legacy framework and commerce schemas, the output loads on the release candidate with snapshots equal to the v3.N baselines. Each rewrite has a test. An unresolvable reference makes it refuse and write nothing | Config owner |
| 3.2 | Finalise the user guide with real versions, and check its error table against release-candidate output | Published | Docs owner |
| 3.3 | Release v3.M, then soak release candidates for at least 4 weeks. Reference apps, flamingo-commerce and known ecosystem modules migrate using the guide. Each migration bug gets a fix and a new release candidate | A soak log is kept in the release tracking issue | Release manager |
| 3.4 | Scheduled latest-CUE job on the v4 line ([Validation](#validation)) | It runs weekly and reports failures as an issue | Config owner |
| 3.5 | Tag v4.0.0 and start the v3 support window | The exit criteria below are met | Release manager |

**How `config cue-migrate` works.** It links only CUE v0.0.15 and runs inside the user's application. It parses
with the parser v3 loads with and prints with the v0.0.15 formatter, which already applies the dual-valid rewrites.

- **Definitions.** It renames `Foo :: x` to `#Foo: x` together with every reference, resolving references against
  the app's own module schemas and all input files at once. It stops and names the reference when a name is
  undeclared, or is a definition in one place and a regular field in another.
- **Other rewrites.** `[e for x in y]` becomes `[for x in y {e}]`, `X = e` becomes `let X = e`, and infix `div`,
  `mod`, `quo` and `rem` become builtin calls. List arithmetic is left to `cue fix` v0.17.1.
- **Inputs and modes.** One run reads every `config*.cue` in every area's config directory, the `.cue` entries of
  `CONTEXTFILE`, and any file arguments. A dry run prints a diff. `--write` applies it, and writes nothing if any file
  fails. `--schemas DIR` writes each module's converted schema, for module authors.

**Exit criteria (GA):** the soak is complete, no migration bug has been open for 2 weeks, flamingo-commerce and the
known ecosystem modules have v4-compatible releases, and the guide is final.

### Timeline

Indicative, in weeks from the start (T):

| Milestone | Earliest |
| --- | --- |
| v3.N released | T+3 |
| v4.0.0-rc.1 | T+6 |
| v3.M released; the soak starts | T+9 |
| v4.0.0 (at least 4 weeks of soak, the last 2 without an open migration bug) | T+13 |
| End of the v3 support window (security and migration fixes) | v4.0.0 + 6 months |

## Validation

- **Golden config snapshots.** `config snapshot` output, committed as testdata and compared by `go test ./...`.
  Values are hashed; keys and Go types are in clear text. They cover `examples/hello-world`, `core/auth/example`
  (`CONTEXT` unset and `static`), `core/oauth/example`, `framework/config/testdata/valid` (`CONTEXT` unset and `dev`),
  an app with every flamingo schema module (a zap and a silentzap variant), and flamingo-commerce's integration test
  project.
- **Allowlist of intended differences.** One line per difference, giving area, key and reason. Each entry is reviewed
  by the config owner and listed in the release notes. The allowlist for v4.0.0 is expected to be empty.
- **Null-override parity table.** A table-driven test pairs every schema shape in flamingo and flamingo-commerce
  (string, bool, number, enum and reference defaults; lists with non-empty, empty and implicit defaults; structs;
  `*null`; no schema) with every YAML form (quoted and unquoted `%%ENV:X%%` with X unset, empty or set;
  `%%ENV:X%%default%%`; `~`; `null`; absent). Each outcome, an error or a value with its Go type, is recorded on v3.N,
  and v4 must reproduce it.
- **Per-module schema probe.** A `config.TryModules` test for each module that has a schema, plus one that loads all
  of them together. A parse-only check is not enough, because two of the 13 failures only show up at build time.
- **Scheduled latest-CUE job (v4 line).** Every week it runs `go get cuelang.org/go@latest`, `go mod tidy` and
  `go test ./...` in flamingo and flamingo-commerce, which covers all the checks above. A failure opens an issue but
  does not block merges. This job is what makes automated CUE upgrades safe again.
- **Users** run the same snapshot comparison for each deployed context (see the guide).

## Deployment, version skew and rollback

- **Config and binary are one artifact.** Converted `.cue` files are v4-only. A v3.N or later binary rejects them at
  boot. Older v3 binaries silently ignore them.
- **Rolling and canary deploys.** v3 and v4 instances must not read the same converted `.cue` files. Give externally
  mounted `CONTEXTFILE` `.cue` files a separate path per major version, or move them to YAML, which means the same on
  both versions. YAML-only apps can roll out gradually.
- **Rollback.** Revert the migration commit (config, `go.mod` and imports) and redeploy. The rollback target must be
  v3.N or later, so that a leftover v4 file fails loudly instead of being dropped.
- **Ordering.** Apps deploy v3.N (or v3.M) to production before they migrate. That way the new loud failures surface
  on v3, where they are cheap to fix.

## What this plan cannot catch

- **Contexts, areas or environments that nobody snapshotted.** Their values are never compared. Strict loading still
  fails loudly there on unparseable files.
- **Snapshots taken with different environment variables than production.** The null-override check still keeps v4's
  boot behaviour equal to v3's.
- **Config that v3 rejected and v4 accepts.** Examples are `v: #A; z: v.c & {zz: 1}` and `package flamingo` in a user
  file. This only affects config written after the upgrade.
- **Pre-existing, version-neutral hazards that this plan leaves as they are.** An unset quoted `'%%ENV:X%%'` becomes
  `""` (an empty secret, for example). YAML merge conflicts between files are discarded. The `.cue` merge silently
  drops quoted and pattern labels and top-level comprehensions in the second and later user files (a dropped `import`
  fails loudly). YAML integers never satisfy `int`-typed keys. Malformed YAML panics.
- **Code or monitoring that matches CUE error text.**

## Decisions made

1. **Target: v0.17.1, in one hop.** Before GA, a newer patch release is adopted only if the full validation passes on
   it. No intermediate version isolates a hazard class, and no compatibility mode exists.
2. **Strict loading comes first.** It ships in v3.N with `--flamingo-config-lenient` as a v3-only opt-out, which v4
   removes. Without it, pre-migrating on v3 or rolling back can silently drop config.
3. **Numbers stay `float64`.** The decode goes through `MarshalJSON` and `json.Unmarshal`, because native `int64`
   fails silently in `, ok` type assertions. `Validate(cue.Concrete(true))` runs first, for error quality; it changed
   no outcome in probes.
4. **The null-override check reproduces v3.** Accepting v4's resolution instead would turn v3 boot failures into
   silently defaulted values.
5. **Package clause on generated input only.** `package flamingo` goes into every generated file and into every module
   schema that lacks one. User files are left alone. A user file's `package flamingo` is accepted, and any other name
   fails, as on v3.
6. **Legacy-alias mismatches become returned errors, never warnings.** `FlamingoLegacyConfigAlias` stays.
7. **prefixrouter takes the disjunction rewrite in v3.N.** The `enabled: bool | *false` alternative would add two
   keys, the new one and its legacy mirror, to every app that loads the module.
8. **Framework `::` schemas are hand-migrated in Phase 2**, verified by the goldens. The tool is for users and comes
   in Phase 3.
9. **No hidden-field bridge for definitions** (`_http` plus `close()`). It changes reference paths inside v3, which is
   a breaking change. It makes users edit twice. It also needs recursive closedness restated by hand.
10. **The migration command is a v3.M app subcommand.** It must link v0.0.15, and it needs the app's module schemas to
    resolve references. Official `cue fix` is not used: v0.17.1 `cue fix` exits 0 and leaves `::` untouched; v0.2.x
    `cue fix` emits bridge fields (`Foo: #Foo @tmpNoExportNewDef(…)`) that leak config keys and does not terminate on
    recursive definitions; and no version sees definitions that live in Go strings.
11. **`cueast.go` stays.** Only its unchecked type assertion changes. Its drop behaviour is version-neutral and
    documented.
12. **Cut from the plan.** A predictive cross-version tool (a v3 snapshot diffed against a v4 snapshot checks the same
    thing). A reverse converter (reverting the commit restores v3). A survey or collection gate (there is no channel
    back from users). A separate `config lint` (strict loading, the `config cue-migrate` dry run and the
    null-override check cover it). Version-bisect CI tests (the scheduled job replaces them).
13. **The deprecated config API stays in v4.0.0.** Removing it needs a separate proposal.
14. **Soak and support.** The soak runs for at least 4 weeks from v3.M. The v3 support window is 6 months after v4.0.0.

## Open questions

| Question | Decides |
| --- | --- |
| Does flamingo-commerce ship a new major version alongside Flamingo v4, or a minor release that requires it? | Commerce maintainer |
| Which third-party modules count as "known ecosystem modules" for the GA gate? | Release manager |
| When is the `cueast.go` merge replaced so that its silent drops become errors (after v4.0.0)? | Config owner |

## Appendix: reproducing the key facts

```bash
# Latest stable CUE, and release dates
go list -m -versions cuelang.org/go
go list -m -json cuelang.org/go@v0.0.15 | grep Time

# Go API breakage: 3 sites in go build, plus 2 in test files
mkdir -p /tmp/cue-probe && git archive HEAD | tar -x -C /tmp/cue-probe && cd /tmp/cue-probe
go mod edit -require=cuelang.org/go@v0.17.1 && go mod tidy
go build ./... ; go vet ./framework/config

# No compatibility mode: parser.ParseFile with parser.Version("v0.0.15") still
# rejects `Foo :: {a: int}` with "expected operand, found ':'"

# Dual-valid rewrite with the official v0.0.15 formatter
printf 'core zap: {a: 1}\n/* c */\nx: 1\n' > l.cue
go run cuelang.org/go/cmd/cue@v0.0.15 fmt l.cue && cat l.cue   # core: zap: {a: 1} / // c / x: 1

# Why the official `cue fix` is not used for definitions
printf 'Foo :: {a: int}\nx: Foo & {a: 1}\n' > d.cue
go run cuelang.org/go/cmd/cue@v0.17.1 fix ./d.cue; echo $?      # 0, file unchanged
printf 'Foo :: {a: int}\nl: [Foo & {a: 2}]\n' > b.cue
go run cuelang.org/go/cmd/cue@v0.2.2 fix b.cue && cat b.cue     # adds Foo: #Foo @tmpNoExportNewDef(…)
printf 'T :: {[string]: N}\nN :: {c?: T}\n' > r.cue
timeout 60 go run cuelang.org/go/cmd/cue@v0.2.2 fix r.cue; echo $?   # 124: does not terminate

# Dependency surface (in the v3 tree and in a ported tree)
go build -o hw ./examples/hello-world && ls -l hw
go list -deps ./framework/config | grep -c '^cuelang.org'
go list -deps ./examples/hello-world | grep -E 'wazero|ociregistry|coder/websocket'   # no output
```

The probes, parity table and goldens added in Phase 1 reproduce the remaining facts. Run on a v4 branch whose schemas
are not yet migrated, `go test ./...` reports the failing schemas.
