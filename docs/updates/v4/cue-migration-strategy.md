# CUE migration analysis and strategy (v4)

Moving `cuelang.org/go` from **v0.0.15** (August 2019) to **v0.17.1** (current stable) for Flamingo v4.

## TL;DR

- **The Go port is tiny.** `go build` against v0.17.1 produces **3 errors**; `go test` reveals a 4th. All three
  production call sites are in `framework/config`, and the mechanical fix is ~40 lines.
- **The framework does not boot on v0.17.1 today.** 11 of 28 module schemas fail to *parse* and 2 more fail to
  *build*; because every schema is added to one `build.Instance`, the first failure aborts the whole config load.
  **13 of 28 schemas (46%) must be edited** — 10 of 21 in flamingo, 3 of 7 in flamingo-commerce.
- **All of that breakage is loud and mechanical.** After a purely mechanical migration, all 28 schemas parse and
  the combined configuration evaluates to **byte-identical values** on both CUE versions.
- **The real risk is a small set of silent changes**, chief among them: a `.cue` file that fails to parse is
  *silently discarded today* and the application boots on defaults. After the upgrade, every unmigrated `.cue`
  file takes that path. The dominant failure mode of a naive migration is **"app starts, config is gone"**, not a
  crash.
- **YAML-only projects are unaffected.** The full documented YAML contract produces identical flattened config on
  both versions. Exactly one `cue` example exists in all of Flamingo's documentation.
- **There is no compatibility mode and no gradual path.** Declaring an older language version re-enables nothing,
  `::` and `#` are mutually exclusive, and the v0.17 binary cannot run the old evaluator. Every `.cue` file is an
  atomic cutover, which is why the validation harness in §7 is the centrepiece of this plan.

## 1. Why this document exists

The CUE bump has been deferred repeatedly and deliberately. Verified in this analysis:

- An upgrade to CUE v0.4.0 was attempted in **July 2021**; the branch `201-update-cue` is still live upstream, tip
  commit `d616ac4` ("core: update cue"). It bumped `go.mod` to v0.4.0 and fixed **only** the space-separated
  label paths in four modules (`core gotemplate engine:` → `core: gotemplate: engine:`), plus `cueast_test.go`.
  It never addressed `::` definitions, cross-file reference scope, or the decode type changes — i.e. it stalled
  after the first and easiest of four breakage classes. That is a good predictor of what an unplanned second
  attempt would do.
- A `renovate/cuelang.org-go-0.x` branch is live upstream, and `renovate.json` has never ignored `cuelang`; the
  freeze is a standing manual veto on each bump, not automation config.
- The recorded project position is that the breaking changes would invalidate existing user configuration, so CUE
  cannot move inside v3 and the work belongs to the v4 milestone. *(Issue/PR numbers reported by the
  investigation — #201, #202, #246, #600 — could not be re-verified through the API from this session, which is
  scoped to this fork; the branch evidence above was verified directly via git.)*

This document replaces "we expect a lot of configuration to break" with measurements.

## 2. Method

Everything below was measured, not recalled. The apparatus:

- Two probe harnesses, one linked against `cuelang.org/go v0.0.15` and one against `v0.17.1`, that build files
  into a `build.Instance` exactly the way `Area.loadCueConfig` does, optionally `Fill` a Go map, `Decode`, and
  print both the decoded JSON **and the concrete Go type of every leaf**. Values alone are not enough: JSON
  renders `float64(3322)` and `int64(3322)` identically, and Flamingo binds config into Dingo *by Go type*.
- A full Flamingo tree with `go.mod` bumped to v0.17.1, in which the port was actually performed and the real
  test suite run.
- All 28 `CueConfig()` bodies mechanically extracted via `go/ast` (including the four built with `fmt.Sprintf`,
  which were expanded by calling the methods) and A/B'd through both versions individually and combined.
- Two independent ports (minimal and hardened), each adversarially re-reviewed by a second party that re-ran the
  builds and tests. Several numbers in earlier drafts were corrected by that review and the corrected figures
  are the ones used here.

Reproduction commands are in the appendix.

## 3. Current state

### 3.1 Where CUE is used

The entire surface is `framework/config` — 5 files, 11 import lines. `flamingo-commerce` has **no direct CUE
import** (`go.mod` lists it `// indirect`); it only contributes schema *strings*.

| File | Use |
| --- | --- |
| `framework/config/area.go` | builds the instance, evaluates, decodes, binds into Dingo |
| `framework/config/loader.go` | parses `.cue` files, `CueDebug` dev API |
| `framework/config/cueast.go` | hand-rolled AST merge of multiple `.cue` files |
| `framework/config/configcmd.go` | `config` CLI dump (`format.Node`) |
| `framework/config/config.go` | `config.Map` / `config.Slice`, the Go-side merge and number normalisation |

### 3.2 The load pipeline

`Area.loadConfig` (`area.go:227-301`) is a fixed 9-step pipeline in which **CUE runs last**, as a
validate-and-default pass over an already-merged Go map:

1. A fresh `build.Instance` per call (`area.go:228`).
2. `AddFile` in order: `flamingo?: modules?: disabled?: [...string]` (`area.go:149`); every module's
   `CueConfig()` under the synthetic filename `pkgpath.TypeName` (`area.go:153-159`); a synthesized
   `flamingo: os: env: {[string]: string, "KEY": "VALUE", ...}` for every process env var (`area.go:161-170`).
3. Deprecated `DefaultConfig()` maps merged (`area.go:175-188`).
4. `Configuration` reset to `Map{"area": <name>}` (`area.go:238`).
5. Go defaults, then all YAML, are `Add`ed — **YAML beats Go defaults** (`area.go:240-245`).
6. `OverrideConfigModule.OverrideConfig` may rewrite anything (`area.go:247-253`).
7. `checkLegacyConfig` copies legacy→new keys so the schema sees the new name (`area.go:255-257`).
8. The generated `purgeNil` file, then `AddSyntax` of the hand-merged user `.cue` AST (`area.go:268-275`).
9. `Build` → `Fill(Configuration)` → `Decode` into a fresh `Map` → `Add` back over `Configuration`
   (`area.go:278-292`), then `checkLegacyConfig` again to copy new→legacy for consumers.

**CUE never removes anything.** Keys it hides (definitions, unset optionals) simply keep whatever the Go merge
produced. Module defaults reach Dingo *only* through the step-9 decode-and-merge-back.

### 3.3 Load-bearing oddities

Four things in this pipeline are surprising, and each one constrains the migration:

- **`purgeNil` (`area.go:259-270`).** For every nil leaf, Flamingo emits `"a": "b": *null | _`, a workaround for
  cuelang/cue#220. This interacts with schema defaults and its behaviour changes on v0.17.1 (§5.3).
- **`cueast.go` implements last-file-wins override**, which CUE unification *cannot express* — unification is
  commutative and idempotent. This is what makes `config.cue` → `config_<context>.cue` → `config_local.cue`
  layering work. It is not a workaround for a missing CUE feature and **must not be deleted** (§6, item R5).
- **The env file is built by string concatenation** with hand-rolled escaping (`esc`, `area.go:219`).
- **Config is bound into Dingo by concrete Go type** (`area.go:331-340`), with extra `int`/`int64` bindings
  registered *only* when the value is `float64`. This is why decode types are a correctness issue, not cosmetics.

## 4. Target state: CUE v0.17.1

### 4.1 Go API delta

`cue.Runtime` and `cue.Instance` still exist as types; their methods are gone.

| Current call | v0.17.1 replacement | Note |
| --- | --- | --- |
| `new(cue.Runtime).Build(bi)` (`area.go:278`) | `cuecontext.New().BuildInstance(bi)` | returns a `cue.Value`; **`.Err()` must be checked explicitly** — the classic porting trap |
| `inst.Fill(m)` (`area.go:283`) | `v.FillPath(cue.Path{}, m)` | see below — this is the only correct mapping |
| `inst.Value().Decode(&m)` (`area.go:289`) | `v.Decode(&m)` | reimplemented; changes number types (§4.3) |
| `inst.Lookup(path...)` (`loader.go:84`) | `v.LookupPath(...)` with `cue.Str` selectors | `.Syntax()` also needs options to stay comparable |
| `cueast_test.go:43`, `:91` | same as `area.go:278` | a 4th compile error, visible only under `go test` |

`build.Context`/`build.Instance`, `parser`, `format`, `errors` and the `ast` node types used by `cueast.go` are
intact. **Flamingo needs no `cue.mod`** — it never calls `load.Instances`.

**`FillPath` vs `Encode`+`Unify` matters more than it looks.** Old `Fill` converted with `nilIsTop=true`, so a Go
`nil` became `*null | _`, not `null`. `FillPath` hardcodes the same behaviour; `Context.Encode` defaults to
`nilIsTop=false`. Since `config.Map` is full of nils, the plausible-looking `Unify(ctx.Encode(m))` **breaks every
nil config value**:

```
FillPath(cue.Path{}, m)                 -> {"host":"example.com","nilish":null}
Unify(ctx.Encode(m))                    -> Err: nilish: conflicting values string and null
Unify(ctx.Encode(m, cue.NilIsAny(true)))-> {"host":"example.com","nilish":null}
```

### 4.2 Language delta

Four constructs that v0.0.15 accepts are removed. All fail at parse time with messages that name neither the
construct nor the fix:

| Legacy (v0.0.15) | v0.17.1 | Error text users will report |
| --- | --- | --- |
| `Foo :: {...}` definitions | `#Foo: {...}` | `expected operand, found ':'` |
| `core oauth: {...}` space-separated label paths | `core: oauth: {...}` | `expected label or ':', found 'IDENT' oauth` |
| `<Name>: {...}` templates | `[Name=string]: {...}` | `expected operand, found ':'` |
| `expr for x in y` trailing comprehensions | `for x in y { expr }` | varies |
| `/* ... */` block comments | `// ...` | `expected operand, found '/'` |

Two further changes are not syntax but scope/semantics:

- **Cross-file references require a package clause.** A file with no `package` clause no longer contributes its
  top-level identifiers to the instance scope. Flamingo adds one anonymous file per module, so any schema
  referencing another module's path fails with `reference "flamingo" not found`. Bisected to the v0.3.1–v0.4.0
  range, i.e. this has been broken since long before v0.17.
- **Optional fields cannot be referenced.** `enabled?: bool` followed by
  `if flamingo.prefixrouter.rootRedirectHandler.enabled {...}` is now `cannot reference optional field: enabled`.
  This kills `framework/prefixrouter`'s own schema (`module.go:59-63`).

**`::` and `#` are mutually exclusive**: v0.0.15 rejects `#Name:`, v0.17.1 rejects `Name ::`. No file can be
valid on both versions, so there is no per-file transition period. Definition *closedness* is identical in both,
so the rewrite itself is semantics-preserving.

### 4.3 Decode and evaluator delta

- **Numbers.** v0.0.15's `Decode` was literally `MarshalJSON` + `json.Unmarshal`, so every number arrived as
  `float64`. v0.17.1 uses a reflective decoder: integers → `int64` (or `*big.Int`), floats → `float64`, bytes →
  `[]byte`. This is a named, version-gated CUE experiment (`DecodeInt64`: preview v0.11, default v0.12, stable
  v0.13) and **cannot be switched off**.
- **Top-level non-concrete values decode to `nil` instead of erroring.** A key declared with a type but never
  given a value used to abort `Load` with a positioned error; now `Load` succeeds and the key is silently `nil`.
  Nested keys still error on both versions.
- **Conflicting module defaults for the same top-level key** used to be a hard boot error; now the disjunction
  silently resolves. `core/zap` and `core/silentzap` both declare `core.zap`.
- **`*null | _` no longer destroys a schema default**, so `key: ~` in YAML against a defaulted key now yields the
  default rather than the empty value.

### 4.4 What does not exist

- **No language compatibility mode.** Declaring an older language version — down to `"v0.0.15"` — re-enables no
  removed syntax. `token.ISA` and `ast.TemplateLabel` are gone from the v0.17.1 tree entirely. Declaring an old
  version only makes the parser *stricter*.
- **No evaluator fallback.** `cuecontext.EvalV2` does not exist at v0.17.1 and `CUE_EXPERIMENT=evalv3=0` is
  rejected. Any old-vs-new comparison needs two binaries.

## 5. Breakage analysis

### 5.1 Flamingo Go code — 4 compile errors, all in `framework/config`

Loud, trivial, covered in §4.1. Not the problem.

### 5.2 Flamingo's own schemas — 13 of 28 (46%)

Measured by extracting every `CueConfig()` body and running it through both versions:

| Cause | Schemas affected |
| --- | --- |
| `::` definitions | 8 |
| space-separated label paths | 3 |
| cross-file reference (no package clause) | 1 (`core/auth`) |
| optional-field reference | 1 (`framework/prefixrouter`) |
| **Total failing on v0.17.1** | **13 of 28** (10 of 21 flamingo, 3 of 7 commerce) |

Commerce casualties: `category`, `checkout`, `product` (all `::`). A legacy-syntax lint of both repositories
found **18 legacy constructs across 8 files** in flamingo and **4** in flamingo-commerce
(`category/module.go:98,101`, `checkout/module.go:107`, `product/module.go:75`).

Two of these are invisible to CI today: `TestModule_Configure` uses `dingo.TryModule`, which never evaluates
`CueConfig()`, and `prefixrouter` is not in the default boot set.

**After mechanical migration, all 28 parse and the combined configuration decodes with zero changed values.**

### 5.3 User configs — where the actual danger is

| # | Change | Loud? | Impact |
| --- | --- | --- | --- |
| U1 | **A `.cue` file that fails to parse is silently discarded** (`loader.go:130-137` drops loader errors unless `DebugLog(true)`) and the app boots on defaults | **SILENT** | After the upgrade every unmigrated `.cue` file hits this path. This is the single most dangerous item in the migration. |
| U2 | Numbers inside lists flip `float64`→`int64`; `Map.Add` normalises scalars at every map depth but **never walks into `config.Slice`** | **SILENT** | `element.(float64)` starts failing or panicking. 16 `config.Slice` injection sites across both repos. Worse, it is positional: the same key stays `float64` if YAML overrides it and becomes `int64` if it comes from a CUE default. Also reaches **maps nested in slices** and **nested lists**, so the fix must be mutually recursive (Slice→Map→Slice). Invisible in the `config` dump, because `Map.Flat()` never descends into `Slice`. |
| U3 | `::`→`#` renames the *reference path*: `core.auth.http` → `core.auth.#http` | Loud, but misleading (`cannot convert non-concrete value ...`) | Every user `.cue` referencing a framework definition breaks. Needs a published rename table and a codemod. |
| U4 | `key: ~` against a defaulted key now yields the module default instead of empty | **SILENT** | Audit every `| *[...]` list default against `~` overrides. |
| U5 | Top-level declared-but-unset key decodes to `nil` instead of failing `Load` | **SILENT** | Fix: `Validate(cue.Concrete(true))` before `Decode`. |
| U6 | Conflicting module defaults for one top-level key silently resolve | **SILENT** | Was a loud boot error. |
| U7 | Integers above 2^53 change value | Silent | Pre-existing in the float64 normalisation; the new path is exact. Decide deliberately. |
| U8 | Removed syntax (`::`, space labels, `<Name>`, block comments) | Loud | Parse errors — but see U1: today they are swallowed. |

**Not affected:** the documented YAML contract. Full flattened config dumps over `testdata/valid` and a real
flamingo-commerce project config are identical on both versions, including the nil path, floats and `%%ENV%%`
defaults. Exactly one `cue` fenced example exists in all documentation (`core/auth/fake/Readme.md:11-37`) and it
breaks verbatim.

### 5.4 Error quality

Mixed, not uniformly worse — but Flamingo makes it worse than it needs to be. `cueError` (`area.go:139-144`)
surfaces only `err.Error()`, and v0.17.1 truncates:

```
old: cue: marshal error at path myapp.ports: empty disjunction: conflicting values int and 9090 ...
new: myapp.ports: 2 errors in empty disjunction: (and 2 more errors)
```

24 of 28 schemas use default-marked disjunctions (169 declarations), so this hits nearly every config error.
`errors.Details(err, nil)` recovers the full text **and exists in both versions**, and
`Value.Validate(cue.Concrete(false))` enumerates the whole tree instead of stopping at the first path (5 errors
across 3 paths vs 3 sub-errors of 1 path in a representative case).

### 5.5 Performance

Not a reason to fear this migration. Measured on the real `config.Load` + `Area.Flat()`:

| Scenario | v0.0.15 | v0.17.1 | Delta |
| --- | --- | --- | --- |
| Single area, 9 real modules | 2.8 ms | 3.1 ms | +11% |
| 50 child areas | 128 ms | 168 ms | +31% |
| Peak RSS (single / 50 areas) | 23.5 / 47.3 MB | 25.5 / 60.3 MB | +9% / +27% |
| Binary size | — | — | +4.7 MB, 6 new indirect deps |

Complexity class is unchanged. The generated os-env file (a wide flat struct) is ~1.9x *faster* on v0.17.1.

## 6. Recommended strategy

The plan splits along a line the evidence makes obvious: **most of the hardening is version-independent and
should ship in v3 now, so that v4 is a small, well-instrumented cutover.**

### Phase 0 — harden on v0.0.15 (ships in v3, non-breaking)

None of this is a CUE bump, so none of it is blocked by the standing v3 veto. Each item is correct on both
versions and can be validated against today's behaviour.

| # | Work | Why now |
| --- | --- | --- |
| R1 | **Stop swallowing `.cue` load errors** (`loader.go:130-137`). Distinguish "optional file absent" from "file exists and is broken" with a sentinel error; propagate the latter. | Closes U1 *before* it becomes the dominant failure mode. Highest value item in the whole plan. |
| R2 | **Make `Map.Add` normalise numbers mutually recursively** through `Slice`→`Map`→`Slice`. | Closes U2. Behaviour-preserving on v0.0.15. |
| R3 | **Fix the Dingo binding loop** (`area.go:331-340`) to register `int`/`int64` bindings for all integral numeric types, not only `float64`. | Removes an entire silent-zero class; correct on both versions. |
| R4 | **`cueError` → `errors.Details`**, plus `Validate(cue.Concrete(false))` for multi-error reporting and origin attribution mapping synthetic filenames back to modules. | `errors.Details` exists in both versions; ship and validate it against current behaviour. |
| R5 | **Add a `CueConfig()` compile test per module** (~15 lines). | CI cannot currently see schema breakage: `dingo.TryModule` never evaluates schemas. This would have caught the prefixrouter and space-label bugs years ago. |
| R6 | **Rewrite space-separated label paths to colon form** in `core/oauth`, `core/gotemplate`, `core/silentzap`. | Verified to parse identically on both versions — free progress, and exactly what the 2021 attempt did. |
| R7 | **Ship the legacy-syntax linter** (`flamingo-cue-lint`) scanning `.cue` files *and* Go `CueConfig()` literals. | Lets the ecosystem inventory its exposure a release before the cutover. |

**Exit gate:** v3 releases with R1–R7; the linter reports a clean framework; no behaviour change in the
cross-version harness.

### Phase 1 — the v4 port

| # | Work |
| --- | --- |
| P1 | Port the 4 call sites (`BuildInstance` + explicit `.Err()`, `FillPath(cue.Path{}, ...)`, `Decode`, `LookupPath`). Do **not** use `Encode`+`Unify`. |
| P2 | Prepend a fixed package clause to every framework-generated `AddFile`, and inject a package decl into the merged user AST. **Must handle a user `.cue` that already declares its own package** — strip, adopt, or emit a migration-grade diagnostic; an unconditional prefix re-breaks a case v0.17.1 would otherwise accept. |
| P3 | Rewrite the 8 `::` schemas to `#`, and fix `prefixrouter`'s optional-field reference to `enabled: bool | *false`. Note this **adds** `flamingo.prefixrouter.rootRedirectHandler.enabled = false` to the decoded config — a real config-surface change to document. |
| P4 | Add `Validate(cue.Concrete(true))` before `Decode` to restore the loud failure for non-concrete top-level keys (U5), and a guard for silently-nulled conflicting defaults (U6). |
| P5 | Decide and document the `purgeNil` semantics explicitly (U4) rather than inheriting the change. |
| P6 | **Do not delete `cueast.go`.** Removing it fails Flamingo's own testdata (`conflicting values 22.0 and 12.0` across `config.cue`/`config_dev.cue`) and the auth example. It needs zero changes for v0.17.1 and handles `#Name` labels correctly. |

**Exit gate:** whole suite green; the cross-version harness reports **0 diffs** on `testdata/valid`, the auth
example, and the full flamingo-commerce schema set.

### Phase 2 — ecosystem and tooling

- Ship `flamingo config migrate` as a codemod. The one working automated chain across the gap is
  **`cue fmt`@v0.0.15** (space labels, `<Name>` templates) → **`cue fix`@v0.2.x** (`::`→`#`) → v0.17.1. Two traps,
  both verified: v0.2.x's fixer emits `Name: #Name @tmpNoExportNewDef(...)` bridge fields that leak **new config
  keys** and must be stripped; and modern `cue fix` **exits 0 and changes nothing** on files it cannot parse, so
  a migration script must never gate on its exit code. There is precedent for shipping migration scripts:
  `docs/updates/v3/renameimports.sh`.
- Publish the definition rename table (`core.auth.http` → `core.auth.#http`, `core.auth.oidc`, `core.auth.fake`, …).
- Migrate flamingo-commerce in lockstep and sequence the release with the other v4 items so downstream projects
  take one upgrade, not three.

### Phase 3 — release

Migration guide (§8), the linter and the diff harness shipped as user-facing commands, and a release note that
names the four removed constructs with their exact error strings so a support search hits the right page.

### Go / no-go gates

- **No-go** if the harness cannot demonstrate 0 diffs on the framework's own testdata and the auth example.
- **No-go** if R1 has not shipped a release earlier — without it the ecosystem's failure mode is silent.
- **Reconsider scope** if the harness shows diffs concentrated in `purgeNil` interactions; that would mean the
  semantics need deciding before the cutover, not during it.

## 7. Validation approach

Because no file can be valid on both versions, "it still parses" proves nothing about meaning. The safety net is
a **cross-version diff harness**, prototyped and proven:

1. **Export a bundle.** Capture every module `CueConfig()` string, every project `.cue` verbatim and in load
   order, the os-env file, the purge-nil file, and the Fill map. Nothing is parsed — so *a bundle exports fine
   from a project whose config no longer parses*, which is exactly the project that needs checking.
2. **Replay through a side-car** built as a nested module pinned to `cuelang.org/go v0.0.15`, carrying verbatim
   copies of `cueast.go` and `config.go` — reproducing the old result means reproducing the old file-merge and
   `Map.Add` normalisation, not just the old evaluator.
3. **Diff values *and* the concrete Go type of every leaf.**
4. `-legacy-dir` supplies the pre-migration spelling per file, since the new binary can only emit the new one.

Proven on real material:

| Corpus | Keys (old/new) | Diffs |
| --- | --- | --- |
| Flamingo `testdata/valid` | 148 / 148 | **0** |
| flamingo-commerce, all 7 schemas migrated | 192 / 192 | **0** |
| `core/auth/example/config` (7 modules, 4 with definitions, 2 layered `.cue`, cross-file refs) | 279 / 279 | **0** |

And proven **not** to be a no-op: with number normalisation off it reports the exact drift
(`sizes[0] float64→int64`, 5 diffs); against a deliberately mis-migrated auth schema it reports **13 diffs**
pinpointed at nested list-element keys a JSON eyeball-diff would miss.

**Recommended user sequence:** `flamingo-cue-lint` (inventory) → migrate → `config-compat --legacy-dir` (prove
equivalence) → upgrade.

> Review note carried forward: the harness prototype's tests printed results instead of asserting them, and
> embedded absolute paths. Before this ships, every harness test must fail red when the feature it covers is
> reverted, and the side-car must be built into a temp dir by the test itself.

## 8. User migration guide (draft)

**If your project only uses YAML** — `config.yml`, `config_<context>.yml`, `routes.yml`, `%%ENV%%`
interpolation, dotted or nested keys — **nothing changes.** Verified byte-identical.

**If your project has `.cue` files:**

1. Run `flamingo-cue-lint ./...` to inventory legacy constructs.
2. Run `flamingo config migrate` to rewrite them. Never hand-run a modern `cue fix` and trust its exit code.
3. **Do not** replace `::` with `:` — that converts a schema-only definition into a required concrete config
   field, which either errors at decode or silently injects new keys.
4. Update references to framework definitions using the published rename table (`core.auth.http` →
   `core.auth.#http`, …).
5. Run `flamingo config-compat --legacy-dir <pre-migration sources>` and require 0 diffs.
6. Audit any `key: ~` override of a key whose schema declares a default (U4), and any `.(float64)` assertion on
   a config list element (U2).

Errors you may see and what they mean:

| Error | Cause | Fix |
| --- | --- | --- |
| `expected operand, found ':'` | `::` definition or `<Name>` template | `#Name:` / `[Name=string]:` |
| `expected label or ':', found 'IDENT' x` | space-separated label path | insert colons |
| `reference "flamingo" not found` | cross-file reference | framework-side; upgrade Flamingo |
| `cannot reference optional field` | `if` guard over an optional field | give the field a concrete default |
| config silently missing | pre-R1 Flamingo swallowed a parse error | upgrade to a release with R1 |

## 9. Risks and open questions

| Risk | Mitigation |
| --- | --- |
| A customer project's config silently loses values | R1 (fail loudly) shipped a release *before* the cutover; harness proves equivalence |
| The codemod corrupts config via name collisions | Rewrite through the AST with scope awareness, never textually; gate on a decoded-key-set diff, which caught exactly this during the investigation |
| `cue fix`@v0.2.x bridge fields leak new keys | Post-process and strip; assert the decoded key set is unchanged |
| The migration stalls like 2021 | The 2021 attempt had no inventory, no harness and no phased plan. Phase 0 delivers value even if v4 slips |
| Unknown constructs in customer configs | The linter, shipped a release early, is the inventory mechanism |

**Open questions**, each with the experiment that settles it:

- How much customer `.cue` exists at all? *Ship R7 and ask the ecosystem to report linter output.*
- Are there `.cue` files that already declare a package clause? *The linter should flag them; P2 depends on it.*
- Should `purgeNil` survive at all? *Run the harness across the ecosystem with and without it and compare.*
- Should numbers stay `float64` forever, or should v4 adopt native types with a documented break? *Currently
  recommended: keep `float64` (opt-in `NumbersNative`), because the failure mode of the alternative is a silent
  `,ok` type-assertion failure rather than a loud error.*

## 10. Appendix

### Reproducing the analysis

```bash
# Latest stable CUE
go list -m -versions cuelang.org/go | tr ' ' '\n' | tail -5

# The Go-side breakage (3 errors; a 4th appears under `go test`)
cp -a <flamingo> /tmp/probe && cd /tmp/probe
go mod edit -require=cuelang.org/go@v0.17.1 && go mod tidy && go build ./...

# A/B a single construct: build two tiny modules, one per CUE version, that
# AddFile the same source into a build.Instance, Decode, and print the
# concrete Go type of every leaf. Type output is mandatory — JSON hides int64/float64.
```

### Removed-syntax quick table

| Legacy | Modern |
| --- | --- |
| `Foo :: {...}` | `#Foo: {...}` |
| `a b c: v` | `a: b: c: v` |
| `<Name>: {...}` | `[Name=string]: {...}` |
| `expr for x in y` | `for x in y { expr }` |
| `/* ... */` | `// ...` |

### Key source references

- `framework/config/area.go:139` (`cueError`), `:149`, `:161-170`, `:227-301`, `:259-270` (purgeNil), `:331-340`
  (typed Dingo bindings)
- `framework/config/loader.go:84` (`CueDebug`), `:130-137` (swallowed loader errors)
- `framework/config/config.go:36-41` (`Slice` conversion), `:75-99` (number normalisation), `:108` (`Flat`)
- `framework/prefixrouter/module.go:59-63` (optional-field reference)
- Upstream branch `201-update-cue`, commit `d616ac4` (July 2021, abandoned v0.4.0 attempt)
