# CUE v0.17 migration for Flamingo v4

Status: proposal

**Decision summary**

- Flamingo v4 moves `cuelang.org/go` from v0.0.15 to v0.17.1 in one step. There is no intermediate version to stop
  at and no compatibility mode.
- The safety net ships first, in **v3.N** on CUE v0.0.15: a `.cue` file that fails to load stops the boot instead of
  being silently ignored, and `config snapshot` records every config key with its Go type and a hashed value.
- **v4.0.0** ports the Go API and adds guards that reproduce v3 results: a package clause on generated files and
  module schemas, decoding through JSON (numbers stay `float64`), v3's text for numbers in interpolations, a
  null-override check, a merge error for references that the `.cue` merge left bound to a dropped declaration, and
  v3 Flamingo module paths accepted in `flamingo.modules.disabled`. It is accepted only if it reproduces the v3.N
  golden snapshots exactly.
- Users convert their `.cue` files with `config cue-migrate`, which ships in **v3.M**. They then compare a v3 snapshot
  with a v4 snapshot for each deployed context. Rollback is a revert of one commit, plus pointing `CONTEXTFILE` back at
  the v3 copies of externally mounted files.

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
- Seven behaviour changes would be silent without a guard, and each gets one: dropped `.cue` files, a null
  overriding a schema default, non-concrete top-level values decoding to nil, integer types inside lists, the text of
  YAML numbers in CUE interpolations, references that the `.cue` merge left bound to a dropped declaration, and
  `flamingo.modules.disabled` entries that still name a v3 Flamingo module path. Every other difference in a
  configuration that loads on v3 fails loudly at boot, except quotients (`/`) printed in interpolations, which only
  the snapshot diff shows. A few things that v3 rejects are accepted by v4 (see
  [What this plan cannot catch](#what-this-plan-cannot-catch)).

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
  `--flamingo-config-log` is set. `loadCueFile` treats any open error as "no file". The loader strips the extension
  of each `CONTEXTFILE` entry and loads both `<entry>.yml` (or `.yaml`) and `<entry>.cue`; an entry that matches no
  file is ignored. `Area.Flat()` discards the load errors of child areas.
- **Coverage.** 15 test files use `config.TryModules`, which runs build, fill and decode. 5 use `dingo.TryModule`,
  which never evaluates `CueConfig()`. 9 of the 20 packages that define a `CueConfig()` have no `config.TryModules`
  test: the root package (`servemodule`), `framework`, `framework/cmd`, `framework/prefixrouter`,
  `framework/systemendpoint`, `core/auth/fake`, `core/auth/http`, `core/auth/example/custom` and `core/silentzap`.

## Target state

v4.0.0 runs on CUE v0.17.1. For every configuration that loads on v3.N, it yields the same keys, values and Go types,
or it fails at boot with an error that names the file position or the config key. Four checks enforce this
continuously: golden snapshots, a null-override parity table, a per-module schema probe, and a scheduled job against
the latest CUE release. The scheduled job gives early warning when a new CUE release changes results.

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
| `` `a-b`: v ``, references `` `a-b` ``, `` y.`a-b` `` (backquoted labels) | `X="a-b": v` with references `X`, or `y["a-b"]`; a backquoted identifier such as `` `c` `` becomes `c` | v0.4.3 | `config cue-migrate` (`cue fmt` v0.0.15 only unquotes identifiers) |

**Version-locked constructs** have no practical dual-valid form, so they are converted at the cutover.

| v3 form | v4 form | v0.17.1 error | Removed in | Converted by |
| --- | --- | --- | --- | --- |
| `Foo :: {…}`, references `Foo`, `a.Foo` | `#Foo: {…}`, `#Foo`, `a.#Foo` | `expected operand, found ':'` | semantics v0.3.0, parser v0.5.0 | `config cue-migrate` |
| `[x*2 for x in y]` | `[for x in y {x*2}]` | `expected ']', found 'for'` | evaluation v0.3.0, parser v0.4.0 | `config cue-migrate` |
| `X = 3` alias | `let X = 3` | `expected label or ':', found …` (the token after the alias) | evaluation v0.4.0, parser v0.5.0 | `config cue-migrate` |
| `a div b` (`mod`, `quo`, `rem`) | `div(a, b)` | `missing ',' in struct literal` | v0.17.0 | `config cue-migrate` |
| `[1] + [2]`, `2 * [1]` | `list.Concat(…)`, `list.Repeat(…)` | `Addition of lists is superseded by list.Concat`, `Multiplication of lists is superseded by list.Repeat` | v0.11.0 | `cue fix` v0.17.1 (literal operands) |

- **No dual-valid definitions.** No dual-valid spelling of a definition keeps its reference path. A hidden field with
  `close()` (`_http`) is valid on both versions, but it renames the path and loses recursive closedness (decision 9).
- **v4-only syntax on v3.** v0.0.15 cannot parse `#Foo`, `[for …]`, `let`, `a!:` or declaration attributes. Before
  v3.N, v3 **silently drops** a file that contains them. The `div()` and `list.Concat`/`Repeat`/`Sort` builtins parse,
  but fail loudly on every v3 release.
- **No useful intermediate version.** Only v0.2.0 to v0.2.2 evaluate both `::` and `#` with v0.0.15 semantics. Those
  releases already reject space-separated labels, templates and block comments, and their `cue fix` is defective
  (decision 10).

**Same text, different result.** The first three are loud on v4; the last is silent. Config that v3 rejects and v4
accepts is listed under [What this plan cannot catch](#what-this-plan-cannot-catch).

- **Closedness inside `if` blocks.** A definition or `close()` value introduced inside `if` is enforced on v0.17.1 but
  not on v0.0.15, whatever the spelling. For example, YAML typos under
  `commerce.checkout.placeorder.contextstore.redis` or `commerce.product.fakeservice.sorting[*]` boot on v3 and fail
  on v4 with `field not allowed`.
- **Wrong-typed `if` guards.** The guard of a top-level `if` with a value of the wrong type (`enabled: "yes"`) is
  silently kept by v3 and rejected by v4. In flamingo and flamingo-commerce this is only prefixrouter, which v3.N
  already makes loud. Guards inside a struct fail on v3 too.
- **Nested `close()` merged through embedding.** v4 rejects it with `field not allowed`.
- **Silent: quotients in interpolations.** v0.0.15 divides with 24 digits and a different exponent form:
  `"\(8080/2)"` prints `4.04E+3` on v3 and `4040` on v4, `"\(7/7)"` prints `1` and `1.0`, and
  `"\(timeout / 1000)s"` with 60000 prints `6E+1s` and `60s`. No guard can reproduce v0.0.15's division; only the
  snapshot diff shows it. Decoded numbers are equal.

### Semantic changes and guards

The v4 decode does not use native `Value.Decode`. It runs `Validate(cue.Concrete(true))`, then `MarshalJSON`, then
`json.Unmarshal` into `config.Map`.

| Change | Trigger | v3 | v4 without guard | Guard | Residual risk |
| --- | --- | --- | --- | --- | --- |
| Unloadable `.cue` file | Parse error (legacy syntax on v4, v4 syntax on v3, typos), unreadable file, or a `CONTEXTFILE` entry with no file | Silently skipped; the app boots without that file's values | Same | Strict loading: v3.N (with an opt-out) and v4.0.0 (without) | v3 users who set `--flamingo-config-lenient`; each skipped file is logged |
| Null over a schema default | YAML null on a key whose schema has a default: unquoted `%%ENV:X%%` with X unset or empty, `~`, or `null` | Boot error for scalar and struct defaults; `[]` for a non-empty list default | The default is used silently: dozens of defaulted keys without a legacy alias in flamingo (e.g. `flamingo.router.notfound`, `core.serve.port`) and in flamingo-commerce (e.g. `commerce.pagination.defaultPageSize`), and list defaults (`commerce.product.fakeservice.deliveryCodes: ~`). Keys with a legacy alias, such as `flamingo.session.secret`, fail in `checkLegacyConfig` instead | Null-override check (below) | None for any schema shape in flamingo or flamingo-commerce. The hypothetical `{a: string \| *"x"} \| *{a: "y"}` fails where v3 booted, which is loud |
| Non-concrete top-level value | A top-level user `.cue` field that is not concrete: an unmigrated reference `H: core.auth.http & {…}`, `T: string`, or conflicting top-level defaults | Load error | Decodes to nil, and `Get` reports the key as present | The JSON decode fails with the position | None observed |
| Integer Go types | Numbers from CUE inside lists (literals, schema defaults) | `float64` | `int64` inside `config.Slice`; scalars are re-normalised by `Map.Add` | JSON decode | None observed |
| Number text | A YAML, environment or Go-default number used in a CUE interpolation or computed label, e.g. `"http://localhost:\(core.serve.port)/"` with port 8080 | `http://localhost:8080/` | `http://localhost:8.08E+3/`: integral values that end in 0 print in exponent form | Every finite, non-zero `float64` or `float32` of the merged map, at any depth of maps and lists, is filled as a CUE float literal with v3's digits (`strconv.FormatFloat(v, 'g', -1, bits)`, plus `e0` when that has no `.` or exponent). Zero, NaN and ±Inf are filled unchanged: v0.17.1 reads the literal `-0e0` as NaN, and NaN and Inf have no CUE literal (2.4) | Quotients (`/`) still print differently ([Syntax](#syntax)) |
| Stale references | A reference whose declaration the `.cue` merge drops or replaces. It happens in any user file, `config.cue` included, as soon as a second `.cue` file is loaded: a later file references a struct label that an earlier file also declares; a file declares the referenced top-level label more than once (two `flamingo:` lines); or a reference names a scalar that a later file overrides. E.g. `k: *flamingo.os.env.X \| "d"` in a `config.cue` with two `flamingo:` lines, next to a `config_local.cue` | Fails with `undefined field`, or with `… (found struct)` in an interpolation, or silently yields the disjunction default, `{}` (for a comprehension, or for a reference to an overridden scalar even inside a disjunction), or nothing for an `if` | Rebinds the reference to a top-level field of that name (usually the intended one, sometimes a different field), or fails with `reference "x" not found` or `structural cycle` | After the last user file of an area is merged, the loader re-resolves the merged file and fails with `ErrStaleReference` on every reference still bound to a dropped declaration (2.2). The `config cue-migrate` dry run reports the same on v3.M | v3 aliases in later files whose expression references a field: `config cue-migrate` refuses them, because their `let` form resolves differently |
| Module path in config | A `flamingo.modules.disabled` entry written for v3 (`flamingo.me/flamingo/v3/…Module`) | The module is disabled | The entry matches no module, and the module stays enabled without a message | An exact match disables the module, as on v3. Otherwise an entry that starts with `flamingo.me/flamingo/v3` followed by `/` or `.` is retried with `v4` in that element; a match logs a deprecation warning, and an entry that still matches no module logs a warning (2.1). `config snapshot` records which modules each area disables (1.1) | A dependency whose module path changed: list its old and new path while a rollback is possible |

**Null-override check.** It runs after `FillPath`, for each key whose merged Go value is nil. That is the same set
that purgeNil marks.

| Schema at the key | Action | Compared with v3 |
| --- | --- | --- |
| none, no default, or default `null` | keep null | already equal |
| open list with only an implicit default (`[...T]`, `[x, ...T]`) | keep | already equal |
| list with an explicit default, e.g. `[...T] \| *[a, b]` | reset to `[]` if the schema accepts `[]`; otherwise fail | v3 result for every shape in flamingo and flamingo-commerce |
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

`cueError` in `area.go` passes only `err.Error()` through, prefixed with one position. On v0.17.1 that text
truncates disjunction errors, which occur in 24 of the 28 schemas. For example, YAML `flamingo.router.timeout: 5000`
fails on both versions, because YAML numbers arrive as floats:

```
v3: conflicting values (int | *60000) and 5000 (mismatched types int and float)
v4: flamingo.router.timeout: 2 errors in empty disjunction: (and 2 more errors)
```

In v4, `cueError` formats every CUE error with `errors.Details`: the errors from parsing, `AddSyntax`, `BuildInstance`,
`FillPath` and `Validate(cue.Concrete(true))`, which runs before `MarshalJSON`. That lists every branch with its
positions, for example `flamingo.router.timeout: conflicting values 5000 and int (mismatched types float and int)`
followed by the schema position `<import path>.<Type>:line:col`. `Validate` also keeps the cause of incomplete values
that `MarshalJSON` drops (`invalid interpolation: cannot reference optional field: host` instead of a bare `invalid
interpolation:`), and prints an unresolved disjunction as `incomplete value 1 | 2` rather than internal syntax. YAML
values have no positions, so an error caused by a YAML value points only into the module schema.

### Cost and dependency surface

Measured with go1.26.5 on linux/amd64:

| Measure | v3 (CUE v0.0.15) | v4 port (CUE v0.17.1) |
| --- | --- | --- |
| `examples/hello-world` binary | 22.83 MB | 28.39 MB (+5.56 MB, +24%) |
| `cuelang.org` packages linked into `framework/config` | 13 | 96 |

- **`go.mod`.** Besides `cuelang.org/go`, it changes `cockroachdb/apd/v2` to `apd/v3` and bumps `golang.org/x/*`.
- **Module graph.** `go mod graph` gains `tetratelabs/wazero` (a WebAssembly runtime),
  `cuelabs.dev/go/oci/ociregistry`, `opencontainers/image-spec` and `coder/websocket`; `go.sum` gains only
  `ociregistry` and `image-spec`. None of them is linked into the binary, and the release notes say so.
- **Environment.** `CUE_DEBUG` is ignored (2.2). In v0.17.1, `CUE_EXPERIMENT` changes nothing Flamingo uses: its
  only non-stable flag, `formatv2`, is not wired in, stable flags cannot be turned off, and invalid values are
  ignored.
- **Runtime cost.** Not measured yet (work item 2.8).

## Plan

**Roles:**

| Role | Owns |
| --- | --- |
| Config owner | Maintainer of `framework/config`. Owns the code work items and decides technical choices inside them |
| Release manager | Tags, release notes, the soak, the GA decision and the v3 support window |
| Commerce maintainer | The flamingo-commerce migration and release, and those of the flamingo modules it depends on (`flamingo.me/graphql`, `flamingo.me/form`, `flamingo.me/pugtemplate`) |
| Docs owner | The user guide and Readme updates |

The phases run in dependency order. No work item depends on a later phase.

### Phase 1: v3.N hardening (v3 minor release, CUE v0.0.15)

| # | Work item | Done when | Owner |
| --- | --- | --- | --- |
| 1.1 | `config snapshot` subcommand. The first line is the header `# flamingo config snapshot 1`. Then it writes one sorted line per area and leaf key with `area`, `key`, the Go type as `%T` prints it (package name, no import path), recursing into every nested slice and map (e.g. `config.Slice[map[string]interface {}{n:float64}]`), and the SHA-256 of the canonical JSON value (HMAC with `--salt`). It omits `flamingo.os.env`, `flamingo.cmd.name` and `cmd.name` (their defaults come from the binary name). It also writes one line per area for each module that `flamingo.modules.disabled` removes, with the `flamingo.me/flamingo/v3` or `/v4` prefix written as `flamingo.me/flamingo/vN`, so v3 and v4 lines compare equal. A child area that fails to load becomes one error line. If the root or the selected area fails, the command exits with the boot error, as every subcommand does | Unit tests cover each rule; the output is byte-stable | Config owner |
| 1.2 | Baselines: golden snapshots of the reference apps ([Validation](#validation)) and of a migration fixture app, `framework/config/testdata/migrate`, which holds the in-repo `.cue` files and fixture modules with copies of the 5 framework and 3 commerce `::` schemas; and the null-override parity table. Record them after 1.1 and before 1.3 to 1.6 | Committed, and `go test ./...` compares against them | Config owner |
| 1.3 | Strict `.cue` loading. `loadCueFile` returns parse errors (file:line:col) and open errors other than "does not exist". `load` and `loadConfigFromBasedir` return these errors instead of passing them to `loadLogged`. A non-empty `CONTEXTFILE` entry with no `.yml`, `.yaml` or `.cue` file fails with `ErrContextFileMissing`. YAML loading is unchanged. `--flamingo-config-lenient` brings back skipping and logs each skipped file | Tests cover a malformed root, context, local, child-area and `CONTEXTFILE` `.cue` file, and a missing entry. The goldens are unchanged | Config owner |
| 1.4 | Legacy-alias failures become errors. The three fatal calls in `checkLegacyConfig` return errors that wrap `ErrLegacyConfigMismatch`, and `loadConfig` returns them. `Area.Flat()` propagates this class of error from child areas; other child errors stay discarded, as today | The parity table and existing tests pass unchanged, and a mismatch still stops `flamingo.App` with the same text | Config owner |
| 1.5 | Dual-valid framework schemas: colon label paths in silentzap, gotemplate and core/oauth, and the prefixrouter disjunction | The goldens are unchanged | Config owner |
| 1.6 | Schema probes: a `config.TryModules` test for each of the 8 uncovered packages other than the root package. `servemodule` is unexported and tests are external packages, so the hello-world golden, which boots through `flamingo.App`, covers it. The golden apps with every schema module (1.2) are the all-schemas probe; one `TryModules` call cannot load zap and silentzap together, because both bind `flamingo.Logger` | All 21 schema modules are covered | Config owner |
| 1.7 | Release notes. They open with: "This minor release can stop apps from booting where config was silently skipped. Before rolling it out, deploy once with `--flamingo-config-lenient` and read the logged skipped files." They cover the new boot failures, `--flamingo-config-lenient`, `config snapshot`, the v4 timeline and the v3 support window | Published with v3.N | Release manager |
| 1.8 | flamingo-commerce adopts v3.N and commits its integration project's snapshot as a golden in its own repository | Committed; commerce CI compares against it | Commerce maintainer |
| 1.9 | `disableModule` handles modules that are not pointers, such as the one `flamingo.WithRoutes` adds, instead of panicking in `reflect.Type.Elem`. An entry that matches no module is then ignored in every app, which rollbacks rely on (2.1) | A test with an unmatched entry in a `WithRoutes` app boots | Config owner |

**Exit criteria:** `CGO_ENABLED=1 go test -race ./...` is green, the goldens and the parity table are committed and
pass, a malformed `.cue` file stops the boot and reports its position, v3.N is tagged, and flamingo-commerce's golden
(1.8) is committed.

### Phase 2: v4.0.0-rc.1 (v4 branch, CUE v0.17.1)

| # | Work item | Done when | Owner |
| --- | --- | --- | --- |
| 2.0 | CI for the `v4` branch: `main.yml` also runs on push and pull request to `v4`, and `golangci-lint.yml` on push to `v4` (it already runs on every pull request). Semanticore stays on master for v3.N and v3.M; v4 pre-release tags are cut by hand from `v4`. Scheduled jobs live on master and check out `v4` until GA, master after. At GA, `v4` merges into master, and a `v3` branch with CI and Semanticore carries the support window | A pull request into `v4` runs the full CI | Release manager |
| 2.1 | Module path `flamingo.me/flamingo/v4`; `cuelang.org/go v0.17.1`; `go mod tidy`. `flamingo.modules.disabled`: an entry that names a loaded module exactly disables it, as on v3. Otherwise an entry that starts with `flamingo.me/flamingo/v3` followed by `/` or `.` is retried with `v4` in that element, so `flamingo.me/flamingo/v3/core/requestlogger.Module` disables the v4 module, and a match logs a deprecation warning. An entry that still matches no module logs a warning and has no other effect. Names are computed for pointer and non-pointer modules alike (1.9). Other paths are never rewritten: ignoring every `vN` element would conflate distinct packages such as `api/v1` and `api/v2` (decision 15) | Builds together with 2.2; tests cover a v3 entry, a v4 entry, an unmatched entry in a `WithRoutes` app, and app modules `api/v1` and `api/v2` in both orders | Config owner |
| 2.2 | Go API port (table above). Fields of type `*cue.Instance` become `cue.Value`, and the context is created with `cuecontext.New(cuecontext.CUE_DEBUG(""))`, so the process environment cannot change evaluation. `cueAstMergeDecls` returns an error instead of making an unchecked type assertion, and keeps the `let` clauses of later files at every struct level (v3 drops aliases in those files from the merge but inlines their expression at each reference, so a constant alias works and its `let` must survive). Stale references: after the last user file of an area is merged (for the root area, after the `CONTEXTFILE` files) and before the first `AddSyntax`, the loader clears the merged file's `Unresolved`, re-runs `astutil.Resolve`, and returns `ErrStaleReference` for every identifier left in `Unresolved` whose `Node` is set and is not an import. It runs once per area, because v0.17.1's build rewrites `Ident.Node` and Flamingo builds the same AST again. The error names file:line:col and reads `this reference points at a declaration that the .cue merge dropped; v3 failed on it or silently used the default or an empty result. Replace it with the value v3 used; or, on v3, declare its top-level label only once in config.cue and reference it from there, or set a later override of a referenced scalar in YAML over a default; then take a new baseline`. The `cueast_test.go` fixtures move from `::` to `#` | The `cueast` tests pass with unchanged assertions. New tests cover a `let` in the second file, top level and nested; `*flamingo.os.env.X \| "d"`, a comprehension and a nested `if` in `config_local.cue` under a schema-declared label; two `flamingo:` lines in `config.cue` next to a `config_local.cue`; a scalar overridden by a later file; no error for a case where v3 and v4 agree (for example an override `a: {y: "2", k: *y \| "fb"}` of `a: y: "1"`); and `CUE_DEBUG=opendef`, which still gives `field not allowed` | Config owner |
| 2.3 | Package clause: `package flamingo` goes into the modules-disabled, env and purgeNil files, and into every module schema that has none. It is inserted as an `ast.Package` declaration into the parsed file, not prepended as a text line, so schema positions keep their own line numbers. User files are left alone | `core/auth` builds with its schema unchanged, a user `.cue` file that reads `flamingo.os.env.X` loads, and a schema error reports the schema string's own line:col | Config owner |
| 2.4 | Decode, number text and error text: `Validate(cue.Concrete(true))`, then `MarshalJSON` and `json.Unmarshal`; numbers are filled with v3's digits (guard table); `cueError` formats every CUE error with `errors.Details` | Tests cover non-concrete top-level values and CUE-sourced numeric lists. Interpolating YAML numbers 80, 8080, 3322, 0.5 and 1.5e6 gives v3's strings (`8080`, `1.5E+6`); `-0.0`, a Go-default NaN and a `float32` list element keep v3's outcome. A YAML value and a `.cue` value that match no disjunct both print every branch with its schema position, and an interpolation of an unset optional field reports `cannot reference optional field` | Config owner |
| 2.5 | Null-override check (rule above) | The parity table from 1.2 passes unchanged | Config owner |
| 2.6 | Strict loading without an opt-out: `--flamingo-config-lenient` is removed | The tests from 1.3 pass | Config owner |
| 2.7 | Hand-migrate the framework `::` material: 5 schemas, 3 example `.cue` files and the `core/auth/fake/Readme.md` block. Update `framework/config/Readme.md` | The reference-app goldens are byte-identical to Phase 1, the `-race` tests are green, and all 21 probes are green | Config owner |
| 2.8 | Boot-cost benchmark: `config.Load` for hello-world and for an app with many child areas, on v3.N and on the release candidate | The benchmark is committed. If `config.Load` on the release candidate is more than 2x slower than on v3.N for either app, the Config owner decides before rc.1 whether to optimise or accept it, and the release notes state the numbers | Config owner |
| 2.9 | Port the flamingo modules that flamingo-commerce depends on (`flamingo.me/graphql`, `flamingo.me/form`, `flamingo.me/pugtemplate`) to `flamingo.me/flamingo/v4` on branches, and tag pre-releases against the release-candidate commit. They import v3's `framework/config`, which does not compile once a build selects CUE v0.17.1 | Each module's tests pass against the release-candidate commit | Commerce maintainer |
| 2.10 | flamingo-commerce on the release candidate: `::` → `#` in `category`, `checkout` and `product` | Commerce unit and integration tests are green against the release-candidate commit (a pseudo-version), and its integration project's snapshot equals its golden from 1.8 | Commerce maintainer |
| 2.11 | Tag v4.0.0-rc.1. The release notes cover the dependency surface, that `CUE_DEBUG` is ignored, error-text changes, the loud changes, and that sessions are not shared across majors ([Deployment](#deployment-version-skew-and-rollback)) | Tagged | Release manager |

**Exit criteria:** rc.1 is tagged. Every golden except the migration fixture's (checked in 3.1) is byte-identical to
its Phase 1 version. The parity table and the probes are green, and flamingo-commerce and the modules from 2.9 are
green on the tagged commit.

### Phase 3: v3.M tooling, soak and v4.0.0

| # | Work item | Done when | Owner |
| --- | --- | --- | --- |
| 3.1 | `config cue-migrate` in v3.M (described below) | A CI job on `v4` runs `config cue-migrate --write --schemas out/` from the v3.M commit on the migration fixture (1.2), loads the output with the v4 loader, and compares its `config snapshot` with the fixture's golden. Each rewrite has a unit test, including a constant alias in a later user file (converted), an alias in a later user file that references a field (refused), a stale reference (refused), a `.cue` file next to a `.yml` `CONTEXTFILE` entry, a child area with its own module and `.cue` file, and a referenced and an unreferenced non-identifier backquoted label, each in the first and in a later file. An unresolvable reference makes it refuse and write nothing | Config owner |
| 3.2 | Finalise the user guide with real versions, and check its error table against release-candidate output | Published | Docs owner |
| 3.3 | Release v3.M, then soak release candidates for at least 4 weeks. At least two applications that use `.cue` files, chosen by the Release manager and named in the release tracking issue (for example maintainers' production apps), follow guide steps 1 to 5 on a release candidate and deploy the result to a non-production environment. If two applications are not named by rc.1 + 2 weeks, the Release manager records this in the tracking issue and decides whether GA waits. A bug in `config cue-migrate` is fixed in a v3.M patch release, a bug in `config snapshot` in a v3.M patch release and a new release candidate, and a bug in v4 in a new release candidate; the affected named applications repeat the guide from step 4a, or from step 2 for snapshot bugs | Each named application has empty snapshot diffs in every deployed context, and each result is logged in the release tracking issue | Release manager |
| 3.4 | Scheduled latest-CUE jobs on the v4 line ([Validation](#validation)), one per repository, running from the default branch and checking out `v4` (2.0); flamingo-commerce's job uses the head of flamingo's `v4` branch | They run weekly, have run once against `v4`, and report failures as issues | Config owner |
| 3.5 | Tag v4.0.0 and start the v3 support window | The exit criteria below are met | Release manager |

**How `config cue-migrate` works.** It links only CUE v0.0.15 and runs inside the user's application, so it needs a
configuration that boots on v3.M. It parses with the v0.0.15 parser using `parser.ParseComments`, applies a copy of
the unexported `fix` pass of `cue fmt` v0.0.15 (for example templates to patterns and block comments to line
comments), and prints with the v0.0.15 formatter (label paths, comprehensions). Together these are exactly the
dual-valid rewrites of `cue fmt` v0.0.15.

- **Definitions.** It renames `Foo :: x` to `#Foo: x` together with every reference, resolving references per area,
  against that area's module schemas and user files (plus the `CONTEXTFILE` files for the root area). It stops and
  names the reference when a name is undeclared, or is a definition in one place and a regular field in another.
- **Other rewrites.** `[e for x in y]` becomes `[for x in y {e}]`, `X = e` becomes `let X = e`, and infix `div`,
  `mod`, `quo` and `rem` become builtin calls. Backquoted labels become quoted labels with a field alias, and
  references to them use the alias or an index. List arithmetic is left to `cue fix` v0.17.1.
- **Refusals.** It also stops, names the position and writes nothing for a stale reference (2.2, same check on the
  v0.0.15 AST, except identifiers bound to a v3 alias, which the next rule covers); for a v3 alias at the top level of a
  later file, or in a struct that an earlier file also declares, whose expression references a field (v3 resolves it
  against the dropped scope, so `let` would change the result); and for a backquoted label with no dual-valid spelling:
  one at the top level of a later file or inside a struct that an earlier file also declares (the merge drops quoted
  labels there), or a bare reference to a backquoted top-level label of a later file.
- **Inputs and modes.** One run reads every `config*.cue` in every area's config directory, for every `CONTEXTFILE`
  entry the `<entry without extension>.cue` file if it exists, and any file arguments. A dry run prints a diff.
  `--write` applies it, and writes nothing if any file fails. `--schemas DIR` writes each module's converted schema to
  DIR in both modes, for module authors and for apps with their own `CueConfig()`; only `--write` touches config
  files. After `--write` the app no longer boots on v3.M, so the command cannot run again until the original files are
  restored.

**Exit criteria (GA):** 3.1 to 3.4 are done, no migration bug is open and none was opened during the last 2 weeks of the
soak. flamingo-commerce, `flamingo.me/graphql`, `flamingo.me/form`, `flamingo.me/pugtemplate` and the other known
ecosystem modules have release candidates whose tests pass against the final v4 release candidate, and in
flamingo-commerce's integration project `go list -deps ./... | grep '^flamingo.me/flamingo/v3/'` prints nothing. Each of
them tags its stable release requiring v4.0.0 within one week of GA (flamingo-commerce the same day), tracked in the
release tracking issue, and the guide links them.

### Timeline

Indicative, in weeks from the start (T):

| Milestone | Earliest |
| --- | --- |
| v3.N released | T+3 |
| v4.0.0-rc.1 (after the modules of 2.9 are ported) | T+6 |
| v3.M released; the soak starts | T+9 |
| v4.0.0 (at least 4 weeks of soak, the last 2 with no migration bug open or opened) | T+13 |
| End of the v3 support window (security and migration fixes) | v4.0.0 + 6 months |

## Validation

- **Golden config snapshots.** `config snapshot` output, committed as testdata and compared by `go test ./...`.
  Values are hashed; keys, Go types and disabled modules are in clear text. They cover `examples/hello-world`,
  `core/auth/example` (`CONTEXT` unset and `static`), `core/oauth/example`, `framework/config/testdata/valid`
  (`CONTEXT` unset and `dev`), and an app with every flamingo schema module (a zap and a silentzap variant).
  flamingo-commerce keeps the golden of its integration test project in its own repository (1.8). The migration
  fixture's golden (1.2) is compared by `go test ./...` on the v3 line and, after conversion, by the 3.1 job on `v4`.
- **One rule for golden differences.** Goldens must stay byte-identical to Phase 1. An intended change is a reviewed
  commit that updates the golden and adds a release-note entry.
- **Null-override parity table.** A table-driven test pairs every schema shape in flamingo and flamingo-commerce
  (string, bool, number, enum and reference defaults; lists with non-empty, empty and implicit defaults; structs;
  `*null`; no schema) with every YAML form (quoted and unquoted `%%ENV:X%%` with X unset, empty or set;
  `%%ENV:X%%default%%`; `~`; `null`; absent). Each outcome, an error or a value with its Go type, is recorded on v3.N,
  and v4 must reproduce it.
- **Per-module schema probe.** A `config.TryModules` test for each module that has a schema. The golden apps with
  every schema module cover them all together. A parse-only check is not enough, because two of the 13 failures only
  show up at build time.
- **Scheduled latest-CUE jobs (v4 line).** Every week a job in each of flamingo and flamingo-commerce runs
  `go get cuelang.org/go@latest`, `go mod tidy` and `go test ./...`, which covers all the checks above; commerce's job
  uses the head of flamingo's v4 branch. A failure opens an issue but does not block merges. The jobs give early
  warning of CUE releases that change results.
- **Users** run the same snapshot comparison for each deployed context and keep it in their CI (guide steps 5 and 8).

## Deployment, version skew and rollback

- **Config and binary are one artifact.** Converted `.cue` files are v4-only. A v3.N or later binary rejects them at
  boot. Older v3 binaries silently ignore them.
- **Rolling and canary deploys.** v3 and v4 instances must not read the same converted `.cue` files. Give externally
  mounted `CONTEXTFILE` `.cue` files a separate path per major version, or move them to YAML on v3 first (that is a
  config change even on v3, so it needs a new baseline). Configuration-wise, YAML-only apps can roll out gradually.
- **Sessions.** The module path is part of every gob-registered type name (for example the session data of
  `core/auth/oauth`, or commerce's checkout context). v3 and v4 instances, and a rollback, cannot read each other's
  sessions in a shared store (redis, file, cookie) or commerce's Redis context store; the affected users lose their
  session. Apps accept a session reset at cutover, and the rc.1 release notes (2.11) say so.
- **Rollback.** Revert the migration commit (config, `go.mod` and imports), point `CONTEXTFILE` back at the v3 copies
  of externally mounted files (keep them mounted until the v3 support window ends), and redeploy binary and config
  together. The rollback target must be v3.N or later, without `--flamingo-config-lenient`, so that a leftover v4
  file fails loudly instead of being dropped. Keep Flamingo's own `flamingo.modules.disabled` entries on their v3
  paths while a rollback is possible: v3 does not match `/v4` entries (releases before v3.N even panic on them in apps
  that use `flamingo.WithRoutes`, 1.9). For a dependency whose module path changed, list the old and the new path.
- **Ordering.** Apps deploy v3.N (or v3.M) to production before they migrate. That way the new loud failures surface
  on v3, where they are cheap to fix.

## What this plan cannot catch

- **Contexts, areas or environments that nobody snapshotted.** Their values are never compared. Strict loading still
  fails loudly there on unparseable files.
- **Snapshots taken with different environment variables than production.** The null-override check still keeps v4's
  boot behaviour equal to v3's. Failures that depend on values, such as closedness inside an `if` branch that only
  production values enable, show up only in a v4 snapshot taken in the deployment itself; the guide asks for that
  run, and otherwise they surface in the canary.
- **Quotients (`/`) in interpolations.** They print with different digits on v4 ([Syntax](#syntax)). Only the
  snapshot diff shows it, and only for values that the snapshot run uses.
- **Hand-converted aliases in later files.** `config cue-migrate` refuses aliases whose expression references a
  field. Converted by hand, their `let` resolves differently from v3, and only the snapshot diff shows it.
- **Config that v3 rejected and v4 accepts.** Examples are `v: #A; z: v.c & {zz: 1}` (lost validation),
  `package flamingo` in the first user file, an `import` in a later user file, and mixed int/float arithmetic that
  v0.0.15 panics on (`y: 1.5e6, n: y * 2`). This only affects config written after the upgrade.
- **Pre-existing, version-neutral hazards that this plan leaves as they are.** An unset quoted `'%%ENV:X%%'` becomes
  `""` (an empty secret, for example). YAML merge conflicts between files are discarded. In the second and later user
  files, the `.cue` merge silently drops package clauses; at the top level or inside a struct that an earlier file
  also declares, quoted and pattern labels, comprehensions and embedded structs; and, inside a struct that an earlier
  file also declares, all but the last of repeated struct-valued labels (e.g. `q: s: a: 1` / `q: s: b: 2`). YAML
  integers never satisfy `int`-typed keys. Malformed YAML panics.
- **A newer CUE selected by a user build.** Another dependency, or a dependency bump, can make a user's build select
  a newer `cuelang.org/go` than v4 was validated with. Flamingo's scheduled job warns maintainers, not users; the guide
  asks users to keep comparing snapshots in CI.
- **Code or monitoring that matches CUE error text.**

## Decisions made

1. **Target: v0.17.1, in one hop.** Before GA, a newer patch release is adopted only if the full validation passes on
   it. No intermediate version isolates a hazard class, and no compatibility mode exists.
2. **Strict loading comes first.** It ships in v3.N with `--flamingo-config-lenient` as a v3-only opt-out, which v4
   removes. Without it, pre-migrating on v3 or rolling back can silently drop config.
3. **Numbers stay `float64`.** The decode goes through `MarshalJSON` and `json.Unmarshal`, because native `int64`
   fails silently in `, ok` type assertions. It is preceded by `Validate(cue.Concrete(true))`, formatted with
   `errors.Details`. That changes no outcome, but keeps the cause of incomplete values that `MarshalJSON` drops and
   prints disjunctions as CUE.
4. **The null-override check reproduces v3.** Accepting v4's resolution instead would turn v3 boot failures into
   silently defaulted values.
5. **Package clause on generated input only.** `package flamingo` is inserted as a declaration into every generated
   file and every module schema that lacks one. User files are left alone. In the first user file, `package flamingo`
   is accepted on v4 (v3 rejects it); any other name fails on v3, and on v4 unless the merge moves a later file's
   top-level non-struct field in front of it. In later user files the merge drops package clauses on both versions.
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
11. **`cueast.go` stays.** 2.2 fixes its unchecked type assertion, keeps `let` clauses from later files (because
    `config cue-migrate` turns aliases into `let`), and rejects stale references. The other difference in resolving the
    merged file, v3 aliases in later files that reference a field, is refused by `config cue-migrate` (3.1). Its other
    drops are the same on both versions.
12. **Rejected alternatives.** A predictive cross-version tool (a v3 snapshot diffed against a v4 snapshot checks
    the same thing). A reverse converter (reverting the commit restores v3). Gating v4 on a survey of users' `.cue`
    usage (there is no channel back from users). A separate `config lint` (strict loading, the `config cue-migrate`
    dry run and the null-override check cover it). Version-bisect CI tests (the scheduled job replaces them).
13. **The deprecated config API stays in v4.0.0.** Removing it needs a separate proposal.
14. **Soak and support.** The soak runs for at least 4 weeks from v3.M. The v3 support window is 6 months after v4.0.0.
15. **Only Flamingo's own v3 module paths are mapped in `flamingo.modules.disabled`.** Ignoring every `vN` path element
    would conflate distinct packages such as `api/v1` and `api/v2`, and could let a stale entry disable a live module.
    The dependencies' module paths cannot be looked up reliably at runtime, because test binaries carry no build
    information.

## Open questions

| Question | Decides |
| --- | --- |
| Do flamingo-commerce, `flamingo.me/graphql`, `flamingo.me/form` and `flamingo.me/pugtemplate` ship new major versions alongside Flamingo v4? Their APIs expose Flamingo types, so a new major is likely. Decide before Phase 2 starts: it sets the import rewrite in guide step 4b | Commerce maintainer |
| Which other third-party modules count as "known ecosystem modules" for the GA gate? Decide by rc.1 | Release manager |
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
go list -deps ./framework/config | grep -c '^cuelang.org'   # 13 on v3; 96 once the code is ported to cuecontext
go list -deps ./examples/hello-world | grep -E 'wazero|ociregistry|coder/websocket'   # no output
```

The probes, parity table and goldens added in Phase 1 reproduce the remaining flamingo facts; the flamingo-commerce
facts come from its golden (1.8) and from 2.10. Run on a v4 branch whose schemas
are not yet migrated, `go test ./...` reports the failing schemas.
