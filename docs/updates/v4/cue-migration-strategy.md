# CUE v0.17 migration for Flamingo v4

Status: proposal, revised

This revision replaces the earlier plan, which accepted loud breaks and required file conversion. Every configuration
that boots on v3 must keep booting on v4 unchanged, and the [user guide](cue-migration-guide.md) follows this design.

## Summary

- Flamingo v4 moves `cuelang.org/go` from v0.0.15 to v0.17.1 and does not ask users to change any file.
- v4 carries two config engines behind one interface. The legacy engine runs on a bundled, renamed copy of CUE v0.0.15.
  It is v3's code, so it gives the same results, quirks included. The modern engine runs on CUE v0.17.1. It translates
  legacy syntax in memory and applies compatibility shims that reproduce v3 results.
- `flamingo.config.engine: auto` is the default. It picks the legacy engine unless a file or schema needs the modern
  one, for example a module schema written with `#Foo`. v5 makes the modern engine the default. The legacy engine is
  removed in v6 at the earliest.
- `config snapshot`, `config compat` and `config cue-migrate` let users and CI check and convert their config. None of
  them is required. v3.18 ships `config snapshot` only. It becomes the oracle for every v4 result.

## Scope and non-goals

### Goals

- Every configuration that boots on v3 boots on v4 unchanged. That covers YAML, `.cue` files, `CONTEXTFILE` entries and
  third-party module schemas, with the same keys, values, Go types and errors.
- Legacy CUE syntax is accepted in all v4 releases, with deprecation warnings that give file and line. The legacy
  syntax includes `Foo :: {}` definitions, space-separated labels (`core zap:`), `<Name>:` templates, `/* */` comments,
  trailing comprehensions, `X = e` aliases, infix `div` and `mod`, list arithmetic and backquoted labels.
- Where CUE v0.17 evaluates a v3 config differently, v4 reproduces v3 by default and reports what would change, with
  file and line. It offers an opt-in for the new semantics.
- Module authors can ship schemas in modern syntax during v4 without breaking users who reference those definitions in
  legacy syntax.
- Tooling converts `.cue` files to modern syntax and proves the result equal before it writes anything.
- The proof comes from real production applications, not only from in-repo examples.

### Non-goals

- v4 does not keep Go API compatibility. Import paths become `/v4`, and the dingo v2 signature change lands in the same
  major.
- v4 does not fix v3 quirks by default. They stay until v5 and are only reported.
- v4 does not remove the deprecated config API (`DefaultConfigModule`, `OverrideConfigModule`, `LoadConfigFile`,
  `GetFlatContexts`). Flamingo finds the module interfaces by their method names, so removing the API would silently
  drop behaviour in modules that implement it. That needs its own proposal.
- v4 does not let users choose the engine per area or per file.

### Anti-goals

- A warning floods the logs. Warnings are deduplicated and printed once per boot.
- Production configuration, or anything derived from it, lands in a public repository.

## Current state

- Flamingo pins `cuelang.org/go v0.0.15` (2019-12-05). The latest stable release is v0.17.1 (2026-07-16). A 2021
  attempt to move to v0.4.0 (upstream branch `201-update-cue`) stalled.
- CUE is imported only in `framework/config`: `area.go`, `loader.go`, `cueast.go`, `configcmd.go` and `cueast_test.go`.
  flamingo-commerce has no direct CUE import and no `.cue` files. It contributes schema strings from 7 modules.
- 21 non-test files implement `CueConfig() string`. 15 test files use `config.TryModules`, which runs build, fill and
  decode. Five test files use `dingo.TryModule`, which never evaluates `CueConfig()`.

### Pipeline in `Area.loadConfig`

1. `Area.loadCueConfig` adds anonymous files: the `flamingo.modules.disabled` schema, every module's `CueConfig()` and a
   synthesized `flamingo.os.env` file with the whole process environment.
2. Go defaults and YAML are merged in Go by `config.Map.Add`, which turns scalar numbers into `float64`.
   `loadYamlConfig` substitutes `%%ENV:X%%` in the raw YAML bytes. If X is unset or empty, `'%%ENV:X%%'` becomes `""`
   and an unquoted `%%ENV:X%%` becomes YAML null.
3. `checkLegacyConfig` maps `FlamingoLegacyConfigAlias` keys. Its `log.Fatal` calls stop the process on a mismatch.
4. Every nil leaf is marked `*null | _` (purgeNil).
5. `cueAstMergeFile` merges the user `.cue` files into one AST, where the last file wins for scalars.
6. The instance is built, filled with the merged map and decoded, and the result is merged back.
7. `Area.GetInitializedInjector` binds every key into Dingo by Go type.

v3 hides two failures. v4 keeps the same outcome and adds a warning:

- `loadLogged` discards every `.cue` load error, parse errors included, unless `--flamingo-config-log` is set.
- `loadCueFile` treats any open error as "no file". A `CONTEXTFILE` entry that matches no file is ignored.

v3 also panics with `reflect: Elem of invalid type dingo.ModuleFunc` on an unmatched `flamingo.modules.disabled` entry
in an app that uses `flamingo.WithRoutes`. v4 ignores the entry, as every other app does. This is the one deliberate
behaviour change.

## What changes

### Go API port

The modern engine uses the CUE v0.17.1 API. The legacy engine keeps the v0.0.15 API on the bundled copy, so its code
changes only by import paths.

| Function | v0.0.15 | v0.17.1 |
| --- | --- | --- |
| `Area.loadConfig` | `new(cue.Runtime).Build(bi)` | `cuecontext.New().BuildInstance(bi)` |
| `Area.loadConfig` | `inst.Fill(m)` | `v.FillPath(cue.Path{}, m)`, then check `.Err()` |
| `Area.loadConfig` | `inst.Value().Decode(&m)` | `Validate(cue.Concrete(true))`, `MarshalJSON`, `json.Unmarshal` into `config.Map` |
| `Load` (`CueDebug` option) | `inst.Lookup(path...)` | `v.LookupPath(...)`; `Syntax()` needs `cue.Final()` |
| `cueast_test.go` (2 sites) | `new(cue.Runtime).Build` | as for `Area.loadConfig` |

- `go build` reports three of the six sites. The `Decode` call still compiles. `go vet` adds the two sites in
  `cueast_test.go`. The `ast`, `parser`, `format` and `errors` packages that `cueast.go` uses stay as they are.
- When combined with the purgeNil marker, `FillPath` handles Go nil values the way v3's `Fill` did.
  `v.Unify(ctx.Encode(m))` does so only with `cue.NilIsAny(true)`.
- No `cue.mod` is needed, because Flamingo never calls `load.Instances`. CUE v0.17.1 needs Go 1.25.0.

### Syntax

v0.17.1 has no language-compatibility switch. `parser.Version("v0.0.15")` still rejects all the removed syntax, and no
older evaluator can be selected. Per-file language versions start at v0.8. Both engines therefore stay in the binary,
and the modern engine needs a translator.

A rewrite is dual-valid when v0.0.15 and v0.17.1 both accept the rewritten text with identical results. A file that
uses only such spellings is class "both" (see [Mixed syntax](#mixed-syntax)) and needs no translation.

| Legacy form | Modern form | Dual-valid rewrite exists | Error on plain v0.17.1 |
| --- | --- | --- | --- |
| `core zap: {…}` (space-separated labels) | `core: zap: {…}` | yes | parse error |
| `<Name>: {…}` template | `[Name=string]: {…}` | yes | parse error |
| `/* … */` | `// …` | yes | parse error |
| `{"\(k)": v for k, v in s}`, `{y: 1 if c}` | `{for k, v in s {"\(k)": v}}`, `{if c {y: 1}}` | yes | parse error |
| `` `a-b`: v `` and references `` `a-b` `` | `X="a-b": v` with references `X`, or `y["a-b"]` | yes | parse error |
| `Foo :: {…}`, references `Foo`, `a.Foo` | `#Foo: {…}`, `#Foo`, `a.#Foo` | no | `expected operand, found ':'` |
| `[x*2 for x in y]` | `[for x in y {x*2}]` | no | `expected ']', found 'for'` |
| `X = 3` alias | `let X = 3` | no | `expected label or ':', found …` |
| `a div b` (`mod`, `quo`, `rem`) | `div(a, b)` | no | `missing ',' in struct literal` |
| `[1] + [2]`, `2 * [1]` | `list.Concat(…)`, `list.Repeat(…)` | no | `Addition of lists is superseded by list.Concat` |

- The legacy engine accepts every legacy form. The modern engine translates every row in memory.
- No spelling of a definition is accepted by both versions and keeps the same reference path. Definitions therefore
  cannot be written dual-valid.
- v0.0.15 cannot parse `#Foo`, `[for …]`, `let`, `a!:` or declaration attributes. v3 silently skips a user `.cue` file
  that contains them. A module schema that contains them fails the boot.
- Only CUE v0.2.0 to v0.2.2 evaluate both `::` and `#` with v0.0.15 semantics. They already reject space-separated
  labels, templates and block comments, so no intermediate version is useful.

### Semantic differences

Some configs mean something different on CUE v0.17.1, even when the text parses. The modern engine either reproduces v3
with a shim or only reports the difference. A reported difference that no shim can reproduce is called a residual. The
legacy engine has none of these differences, because it is v3's code.

| Difference | v3 | Plain v0.17.1 | Modern engine in v4 |
| --- | --- | --- | --- |
| Null over a schema default (unquoted `%%ENV:X%%` with X unset, `~`, `null`) | boot error for scalar and struct defaults, `[]` for list defaults | the default is used silently | shim reproduces v3 |
| Number text in interpolations (`"http://localhost:\(core.serve.port)/"`) | `8080` | `8.08E+3` | shim fills every finite non-zero float as a CUE float literal with v3 digits |
| Integral number in a float field | accepted, YAML numbers are `float64` | `1` does not unify with a float field | same shim |
| Integer types in lists, non-concrete top-level values | `float64`, load error | `int64`, decodes to nil | JSON decode after `Validate(cue.Concrete(true))` |
| Closed definition inside an active `if` | open | `field not allowed` | shim keeps definitions in `if` bodies open |
| `if` on an optional field (prefixrouter) | works | `cannot reference optional field` | shim handles guard and source as v3 |
| Reference to a declaration that the file merge dropped | `undefined field`, the default or an empty result | rebinds to another field, or fails | shim resolves it as v3 does |
| `/v3` module path in `flamingo.modules.disabled` | disables the module | matches nothing | mapped on both engines, with a warning |
| Quotient in an interpolation (`"\(8080/2)"`) | `4.04E+3` | `4040` | residual, decoded values are equal |
| List arithmetic with two reference operands in a disjunction | works | fails | residual |
| `>3 & <5 & int` | 4 | not concrete | residual |
| Wrong-typed `if` guard (`enabled: "yes"`) | kept silently | rejected | residual |

- The shim passes zero, NaN and ±Inf through unchanged. v0.17.1 reads the literal `-0e0` as NaN, and CUE has no literal
  for NaN or Inf. `float32` values are handled too.
- Null over a default needs a shim because unification cannot express the rule. Even with a v3-style marker
  (`*null | bool | number | string | bytes | [...] | {...}`), v0.17.1 resolves to the default.

### Framework and ecosystem schemas

13 of the 28 framework and flamingo-commerce schemas fail on v0.17.1 and on v0.18.0-rc.3 alike: 10 of 21 in Flamingo
and 3 of 7 in flamingo-commerce.

| Cause on v0.17.1 | Schemas | Handling in v4 |
| --- | --- | --- |
| space-separated labels | `core/silentzap`, `core/gotemplate`, `core/oauth` | translated |
| `if` on the unset optional `enabled` | `framework/prefixrouter` | shim |
| cross-file reference, no package clause | `core/auth` (`WebModule` reads `flamingo.debug.mode`) | package scope |
| `::` definitions | `core/auth/http`, `core/auth/fake`, `core/auth/oauth`, `core/auth/example/custom` (2) | translated |
| `::` definitions | flamingo-commerce `category`, `checkout`, `product` | translated |

- On plain v0.17.1, one failing schema stops the config load of every app that includes that module, YAML-only apps
  included. The modern engine builds all schemas and user files of an area in one package scope, which also fixes the
  cross-file references.
- Flamingo's own schemas stay legacy or dual-valid during v4, so existing users stay on the legacy engine.
- Four schemas are built with `fmt.Sprintf` (`core/zap`, `core/silentzap`, `core/security`, `framework/cmd`). The
  classifier and translator run at runtime, so they see the final string.
- Users can reference 11 definition paths: 5 under `core.auth`, 2 in `core/auth/example/custom` and 4 in
  flamingo-commerce. The translator resolves references to them in either syntax.

### Error quality

`cueError` in `area.go` passes only `err.Error()` through, prefixed with one position. On v0.17.1 that text truncates
disjunction errors, which occur in 24 of the 28 schemas. For example, YAML `flamingo.router.timeout: 5000` fails on both
versions, because YAML numbers arrive as floats:

```
v3: conflicting values (int | *60000) and 5000 (mismatched types int and float)
v0.17.1: flamingo.router.timeout: 2 errors in empty disjunction: (and 2 more errors)
```

The legacy engine keeps v3's text byte for byte. The modern engine formats every CUE error with `errors.Details`. That
lists every branch with its positions, for example `flamingo.router.timeout: conflicting values 5000 and int
(mismatched types float and int)` followed by the schema position `<import path>.<Type>:line:col`.

### Cost and dependency surface

All figures in this section were measured on a prototype. They are to be re-measured in the implementation.

`examples/hello-world`, same toolchain for all three builds:

| Build | Binary |
| --- | --- |
| v3 (CUE v0.0.15) | 30.46 MB |
| modern engine only | 35.99 MB (+5.5 MB) |
| both engines | 36.90 MB (+6.4 MB) |

- A renamed copy of CUE v0.0.15 links next to v0.17.1 in one binary, because `apd` v2 and v3 coexist.
- The bundled copy is frozen 2019 code. It depends on `apd/v2` and `x/xerrors`, so it adds govulncheck surface. It gets
  security and build patches only.
- `cuelang.org` packages linked into `framework/config`: 13 on v3 and 96 on the modern engine.
- `go mod graph` gains `tetratelabs/wazero`, `ociregistry`, `image-spec` and `coder/websocket`. None is linked into the
  binary.
- `config.Load` takes 3 to 4 ms on v3. The modern engine took 14 to 20 ms, about half of it in the uncached translator.
- A default boot does not evaluate the modern engine. It still pays for the larger binary and for one classification
  pass over all files and schemas.

## Architecture

Two config engines sit behind one interface inside `framework/config`. Both produce the same `config.Map`. Dingo
bindings and everything after the load are unchanged.

### Legacy engine

It is v3's pipeline, unchanged: `loadCueConfig`, YAML merge, `checkLegacyConfig`, `cueast` merge, build, fill, decode.
It runs on a renamed copy of CUE v0.0.15, for example `flamingo.me/flamingo/v4/internal/cuelegacy`, so that it links
next to `cuelang.org/go v0.17.1`.

### Modern engine

It runs on CUE v0.17.1 and adds four parts.

The translator converts legacy user files and legacy module schemas to modern syntax before the build. It works in
memory and caches per process. It resolves every reference in a user file against the module schemas of the area. A
legacy reference `httpcache.Memory` becomes `httpcache.#Memory` when the module declares `#Memory`. A modern reference
to a module that still declares `Memory ::` is resolved the same way.

The file merge is v3's `cueast` merge. A later file's scalar replaces an earlier scalar, and struct nodes merge in
place. Files are not unified as separate CUE files, because unification fails with `conflicting values` where a later
file replaces a scalar.

The compat shims reproduce the v3 results listed under [Semantic differences](#semantic-differences). The key
`flamingo.config.compat` defaults to `true`. With `false` the shims are off and the engine gives pure v0.17 semantics.
`false` becomes the default no earlier than v5.

The warning reporter is described under [Data flow and errors](#data-flow-and-errors).

### Engine selection

The engine is chosen per application. The key is `flamingo.config.engine` with the values `auto`, `legacy` and
`modern`. The flag is `--flamingo-config-engine` and the environment variable is `FLAMINGO_CONFIG_ENGINE`.

- `auto` is the default. It picks the legacy engine if every file and schema of every area parses as legacy. It picks
  the modern engine as soon as any modern-only file or schema is present, and logs one line that names the file.
- v4 classifies every file and schema before it picks the engine. That includes the static `NewArea` tree, every module
  that `resolveDependencies` adds, and the modules that `flamingo.modules.disabled` removes later, because v3 loads
  their schemas too.
- `NewApplication` chooses the engine once, before `config.Load`.
- The highest source wins: the flag, then the environment variable, then the key in the root area's YAML. The YAML files
  are parsed before any CUE runs, so no extra pass is needed. Child areas cannot set the key. `flamingo.config.compat`
  is read in the same way, with `--flamingo-config-compat` and `FLAMINGO_CONFIG_COMPAT`.

### Mixed syntax

Each `.cue` file and each module schema is classified.

| Class | Legacy engine | Modern engine |
| --- | --- | --- |
| legacy-only | as v3 | translated |
| modern-only | not possible, `auto` switches | as is |
| both (dual-valid, most files) | as v3 | as is |
| neither (old and new mixed in one file, or a typo) | user `.cue` file: skipped with a warning (v3 skipped it silently) | user `.cue` file: skipped with a warning |

Mixing across files works in the modern engine. A name declared as both `#x` and `x` is refused as ambiguous. A module
schema that is neither fails the boot on v3.

### Components and tools

- `config snapshot` prints, per area, each key with its Go type and the SHA-256 of its canonical JSON value, plus the
  module list after `flamingo.modules.disabled`, including unmatched entries. It ships in v3.18 as the oracle.
- `config compat` runs both engines and reports every key that would differ, with its cause. It exits non-zero, so CI
  can use it.
- `config cue-migrate` converts `.cue` files (see below).
- `config.TryModules` gets an option that runs a module set against both engines, for module authors' tests.

Module authors keep `CueConfig() string`. Legacy strings keep working. A `#` schema needs the modern engine, which
`auto` selects.

The snapshot format is frozen in v3.18, and v3.18 and v4 share one golden test of it. Areas use their flat name
(`root`, `root/<child>`). Keys are flattened at non-empty maps. Values are canonical JSON with sorted keys, floats in
Go's shortest `'g'` text, `-0` as `-0`, and NaN and ±Inf as the strings `"NaN"`, `"+Inf"` and `"-Inf"`.

### `config cue-migrate`

- It rewrites `.cue` files to modern syntax: definitions and references, label paths, comments, comprehensions, aliases,
  `div` and backquoted labels. It reads legacy input with the bundled v0.0.15 parser and resolves references against
  the app's own module schemas.
- A dry run prints a diff. `--write` applies it. `--schemas DIR` writes converted module schemas for module authors.
- It refuses unsafe cases and names the position.
- It verifies every rewrite. The modern engine on the result must equal the legacy engine on the original. If any
  difference remains, it writes nothing.

The official `cue fix` is not used. v0.17.1 `cue fix` exits 0 and leaves `::` untouched. v0.2.x `cue fix` emits bridge
fields (`Foo: #Foo @tmpNoExportNewDef(…)`) that add unwanted config keys. It also does not terminate on recursive
definitions. No version sees definitions that live in Go strings.

### Data flow and errors

Per area, v4 collects the inputs, classifies them, runs the engine chosen for the application and returns a
`config.Map`.

Warnings are buffered and printed after the logger is configured. Each has a code of the form `CUE001`, a file and line,
and the fix. The codes are assigned in the implementation. A warning has one of four kinds. It reports a legacy
construct with its modern form, a skipped file or `CONTEXTFILE` entry, a deprecated value such as a `/v3` module path,
or a shim that changed the result compared with `flamingo.config.compat: false`.

- A boot with the legacy engine prints syntax deprecation warnings and one line that points to `config compat`.
- A boot with the modern engine prints syntax warnings and a warning for every shim that changed the result.

| Situation | v4 behaviour |
| --- | --- |
| Unparseable user `.cue` file | skipped with a warning (v3 skipped it silently) |
| Missing `CONTEXTFILE` entry | ignored with a warning |
| Legacy engine error | byte-identical to v3 |
| Modern engine error | `errors.Details` with positions |
| Translator failure | `auto` falls back to legacy if nothing modern-only exists, otherwise a boot error with file, construct and fix |
| Legacy-alias `log.Fatal` | returned error with the same text |
| Unmatched `flamingo.modules.disabled` entry in a `flamingo.WithRoutes` app | ignored, as in every other app (v3 panics) |

## Validation

A v3 oracle records the expected outcome of every case. It is the real v3.17.x config code built from its tag, and
`config snapshot` from v3.18 on.

1. Golden snapshots cover hello-world, `core/auth/example` (two contexts), `core/oauth/example`,
   `framework/config/testdata/valid` (two contexts), an app that loads every schema module and the flamingo-commerce
   integration project. The legacy engine is byte-identical to the oracle. The modern engine is identical except for
   the documented quotient text.
2. A compatibility corpus in `framework/config/testdata/compat/` holds every legacy construct, every semantic class,
   every mixing scenario and all four file classes, with the oracle outcomes and the expected warning codes.
3. A translator round trip runs over the corpus and over all framework, commerce and third-party schemas. It covers
   the `cue-migrate` self-verification, its refusals and its idempotence.
4. Differential fuzzing feeds YAML numbers (including -0, NaN and large integers), nulls and lists to random schema
   shapes.
5. `TryModules` runs on both engines for the 21 Flamingo schemas, the 7 commerce schemas and the third-party ones.
6. Maintainers run a private suite on 11 production applications. It runs the 39 deployed contexts, the 10 CI
   integration contexts and one run without environment per application, with deterministic placeholders for secrets.
   The v3 oracle, the legacy engine and the modern engine must produce equal snapshots. Its inputs and outputs never
   enter this repository.
7. CI in this repository runs the oracle job, the corpus and the goldens with `-race`. It also runs a weekly job
   against the latest CUE release for the modern engine, and govulncheck including the bundled copy.

### What the real applications showed

Research on real applications found the following. The applications load config from the image's `config/` tree plus
YAML files passed through `CONTEXTFILE`. No `.cue` file reaches a pod through Helm values or `CONTEXTFILE`, but the
image's `config/` tree still holds `config.cue`. The pod environment is part of the config, because every variable
lands in `flamingo.os.env.*`. The v3 baseline loads with zero errors in all 49 contexts.

| Finding | Count | Consequence for the design |
| --- | --- | --- |
| User `.cue` files that parse on both CUE versions | 30 in 10 apps | legacy syntax in user files is not the main risk |
| References from a user `config.cue` to legacy module definitions (`httpcache.Memory`, `Redis`, `Cache`) | 27 lines in 10 apps, plus 2 app-local schemas | the translator resolves references across syntaxes |
| A later `.cue` file replaces a scalar (`session.backend`) | 8 apps in the CI context and local dev | the modern engine keeps v3's file merge |
| Closed definitions inside an active `if` (a third-party cart module, `Redis` in commerce checkout) | 9 apps | shim keeps definitions in `if` bodies open |
| Integral numbers in float fields (Helm renders `1.0` as `1`) | 2 apps in deployed contexts | number-fill shim |
| Distinct module schemas loaded | 128: 13 legacy-only, 0 modern-only, 115 both | `auto` picks the legacy engine for every app today |
| prefixrouter `if` on optional `enabled?` | every app | shim |
| Cross-file schema references without a package clause | several third-party schemas, core/auth | one package scope per area |
| Unquoted `%%ENV:X%%` on a key with a schema default | 4 apps, dormant | v3 fails at boot, v4 must too |
| Legacy alias keys mirrored in the output | most apps | `checkLegacyConfig` stays byte-identical |
| Quotients, `div`, `mod`, list arithmetic, aliases, backquoted labels, wrong-typed guards, `flamingo.modules.disabled`, a double `flamingo:` label | none | the known quotient residual does not occur |

The baselines were built from current application source.

### Ecosystem proof before the beta

flamingo-commerce and the third-party modules still import `flamingo.me/flamingo/v3` during the alpha. Schema-level
checks use schema strings that a script extracts from the pinned module versions into testdata. The commerce
integration golden and the private suite on v4 run from the beta, when the ecosystem pre-releases exist.

## Releases and rollout

One Flamingo v4 carries both this work and the dingo v2 signature change. Every module and application moves to the
`/v4` import path once. The config engines land first, and the signature change follows when dingo v2 is published.
`gob.Register` puts the module path into the name of Flamingo's own session types, such as the auth session data. A
session that holds such a value and was written by one major cannot be decoded by the other.

1. v3.18 adds `config snapshot` and changes no behaviour. It is released from master like any other minor release. It
   lets the private suite run before v4 exists.
2. A `v4` branch is created in the main repository. It receives the `/v4` module path rename first and then the engines.
   Work happens on feature branches against it.
3. v4.0.0-alpha contains the legacy engine on the bundled copy, the `/v4` paths and engine selection with `auto`. A
   later alpha adds the modern engine as opt-in, `config compat` and `config cue-migrate`.
4. v4.0.0-beta contains the dingo v2 signatures (`NewApplication`, `App`, `WithCustomLogger`, `config.NewArea`,
   `config.Area`, `GetInitializedInjector`, `config.TryModules` and every `Configure`) and Go 1.27. It also has
   pre-releases of flamingo-commerce, flamingo-commerce-contrib, graphql, form, httpcache, opentelemetry and
   pugtemplate. Modules keep their current schemas. Third-party modules can test against the beta.
5. v4.0.0 is tagged when the exit criteria below are met.
6. During v4.x, deprecation warnings stay on, `auto` stays the default and Flamingo's own schemas stay legacy or
   dual-valid. Ecosystem modules may switch their schemas to `#` once the cross-syntax translator is released.
7. v5 makes the modern engine and the new semantics the default. Legacy syntax still loads, with warnings. The legacy
   engine is removed no earlier than v6.

### Exit criteria for v4.0.0

- The legacy engine shows no difference from the oracle.
- The modern engine shows no unexplained difference from the oracle.
- The flamingo-commerce goldens are equal on both engines.
- The private suite is equal on both engines for every context. It runs at the release candidate against the then
  current application source.

A warning-code reference lists every code with its fix. The guide gets a "what changes in v5" section, and a note for
module authors on when to switch a schema to `#` and how to run `TryModules` on both engines.

## What this design cannot catch

The legacy engine is v3's code, so it has no residuals. The modern engine has four, listed below, and two more kinds of
risk.

- Quotients in interpolations print with different digits. `"\(8080/2)"` is `4.04E+3` on v3 and `4040` on v0.17.1.
  v0.0.15 divides with 24 digits, and no shim can reproduce that. Decoded numbers are equal.
- List arithmetic with two reference operands inside a disjunction fails on v0.17.1.
- `>3 & <5 & int` gives 4 on v3 and is not concrete on v0.17.1.
- A wrong-typed `if` guard, such as `enabled: "yes"` on a top-level `if`, is kept on v3 and rejected on v0.17.1. A shim
  handles the unset optional `enabled` field of prefixrouter, but not a wrong-typed value.
- v0.17.1 accepts some config that v3 rejects. Examples are `v: #A; z: v.c & {zz: 1}` (validation is lost),
  `package flamingo` in the first user file, and mixed int and float arithmetic, which panics on v0.0.15. This affects
  only config written after the upgrade.
- Code or monitoring that matches CUE error text can break. The legacy engine keeps v3's text, and the modern engine
  words errors differently.

`config compat` reports each of the first four cases with its cause. It sees only the contexts and environments that
someone runs it for. A value that only production sets, for example one that enables an `if` branch, shows up only in a
run inside the deployment.

Some v3 problems stay unchanged on both engines. An unset quoted `'%%ENV:X%%'` becomes `""`. YAML merge conflicts
between files are discarded. The `.cue` merge silently drops package clauses and some labels in later files. YAML
integers never satisfy `int`-typed keys. Malformed YAML panics.

A user build can also select a newer `cuelang.org/go` than v4 was validated with. The weekly CI job warns maintainers,
not users.

## Decisions made

1. v4 has two engines instead of one engine with shims. Only the bundled copy reproduces v3 quirks, and a prototype of
   the modern engine alone still had one silent difference. The price is a frozen dependency, about 6 MB and two
   pipelines to maintain.
2. An unparseable user `.cue` file is skipped with a warning, not turned into a boot error. v3 boots with such files,
   and an error would break a working application.
3. Legacy-alias mismatches become returned errors with the same text. The outcome stays a failure.
4. Numbers stay `float64`. The decode goes through `MarshalJSON` and `json.Unmarshal`, because native `int64` fails
   silently in `, ok` type assertions.
5. Only Flamingo's own `/v3` module paths are accepted in `flamingo.modules.disabled`. Ignoring every `vN` element
   would conflate distinct packages such as `api/v1` and `api/v2`. The mapping is applied to the module list, for both
   engines.
6. The `cueast` file merge stays. 8 applications rely on it in the CI integration context and in local development, and
   plain unification fails with `conflicting values`.

The earlier plan had strict `.cue` loading, `ErrStaleReference` errors, required file conversion, a prefixrouter schema
rewrite and a `BuildInstance().Err()` check before the fill. All five are dropped, because each can turn a boot that
works on v3 into a failure.

## Open questions

- What is the package layout inside `framework/config`, and what does the engine interface look like?
- How does the translator cache, and how is its output represented?
- How are the warning codes numbered and worded?
- How are the CI jobs laid out in this repository?

## Appendix: reproducing the key facts

```bash
# Latest stable CUE, and release dates
go list -m -versions cuelang.org/go
go list -m -json cuelang.org/go@v0.0.15 | grep Time

# Go API breakage: 3 sites in go build, plus 2 in test files (go vet)
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

Once the corpus, the goldens and the `TryModules` tests exist, they reproduce the remaining facts.
