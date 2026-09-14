# CUE migration analysis and strategy (v4)

Moving `cuelang.org/go` from **v0.0.15** (August 2019) to **v0.17.1** (current stable) for Flamingo v4.

## TL;DR

- **The Go port is tiny.** `go build` against v0.17.1 produces **3 errors**; `go test` reveals a 4th. All in
  `framework/config`. Roughly a day of work.
- **The framework does not boot on v0.17.1 today.** **13 of 28 module schemas fail** — 11 at parse, 2 at build.
  Because every schema is added to one `build.Instance`, the first failure aborts the whole config load.
- **That breakage is loud and mechanical.** After migration all 28 schemas parse *and build*, and the combined
  configuration decodes to **byte-identical values and leaf Go types** on both versions.
- **The real migration is a one-way syntax cutover sitting on top of a loader that hides failure.**
  `loader.go:130-137` discards every `.cue` load error unless debug logging is on, so an unparseable file is
  silently dropped and the app boots on schema defaults. The bug is **pre-existing**; the version bump converts it
  into a mass-casualty event by invalidating six years of previously valid syntax.
- **The most dangerous single change is security-relevant**: an unset `%%ENV:VAR%%` on a key whose schema has a
  default is a **hard boot failure on v0.0.15** and **boots silently on the default on v0.17.1** — including
  `flamingo.session.secret`, whose schema default is the literal `"flamingosecret"`.
- **YAML-only projects are unaffected.** YAML never reaches the CUE parser. This is probably most projects.
- **There is no compatibility mode, no dual-valid dialect, and no useful stepping-stone version.** Every `.cue`
  file is an atomic cutover, which is why the validation harness in §7 is the centrepiece.

**Recommendation:** ship the migration machinery on **v3.x, running on CUE v0.0.15**, before v4 exists. The
hardening is independently valuable and none of it is a CUE upgrade. Then bump once, with a tool that can prove
a project's config still means the same thing.

## 1. Why this document exists

The CUE bump has been deferred repeatedly and deliberately. Verified directly via git:

- An upgrade to CUE v0.4.0 was attempted in **July 2021**; the branch `201-update-cue` is still live upstream, tip
  commit `d616ac4` ("core: update cue"). It bumped `go.mod` to v0.4.0 and fixed **only** the space-separated label
  paths in four modules (`core gotemplate engine:` → `core: gotemplate: engine:`), plus `cueast_test.go`. It never
  touched `::` definitions, cross-file reference scope, or the decode type changes — it stalled after the first
  and easiest of four breakage classes. That is a good predictor of what an unplanned second attempt would do.
- A `renovate/cuelang.org-go-0.x` branch is live upstream, and `renovate.json` has never ignored `cuelang`; the
  freeze is a standing manual veto on each bump, not automation config.
- The recorded project position is that the breaking changes would invalidate existing user configuration, so CUE
  cannot move inside v3 and the work belongs to the v4 milestone. *(Issue/PR numbers reported by the
  investigation — #201, #202, #246, #600 — could not be re-verified through the API from this fork's session; the
  branch evidence above was verified directly.)*

### 1.1 Why six years of broken-on-arrival schemas went unnoticed

This is the root cause, and it explains every other finding: **CI cannot see schema breakage.** Module tests use
`dingo.TryModule`, which never evaluates `CueConfig()`. A schema can be syntactically dead for years without a
single test noticing — which is exactly what happened to `core oauth:` (space-separated labels, broken since
CUE v0.4.x) and to `framework/prefixrouter`'s optional-field reference. Any plan that does not add a per-module
schema **build** probe will reproduce this failure mode against the next CUE release.

## 2. Method

Everything below was measured. The apparatus:

- Paired probe harnesses, one linked against `cuelang.org/go v0.0.15` and one against `v0.17.1`, building files
  into a `build.Instance` exactly the way `Area.loadCueConfig` does, optionally filling a Go map, decoding, and
  printing the decoded JSON **and the concrete Go type of every leaf**. Types are mandatory: JSON renders
  `float64(3322)` and `int64(3322)` identically, and Flamingo binds config into Dingo *by Go type*.
- A full Flamingo tree bumped to v0.17.1 in which the port was actually performed and the real suite run.
- All 28 `CueConfig()` bodies extracted mechanically via `go/ast` (including the four built with `fmt.Sprintf`,
  expanded by calling the methods) and A/B'd individually **and in the full-module-set combination** — cross-file
  scope only reproduces with multiple files present.
- Two independent ports (minimal and hardened), four competing migration strategies scored by three judges, and
  an adversarial completeness critic that re-ran the experiments. Several figures in earlier drafts were wrong
  and were corrected by that pass; the corrected ones are used here. Where a claim is unverified, it says so.

## 3. Current state

### 3.1 Where CUE is used

The entire surface is `framework/config` — 5 files, 11 import lines. `flamingo-commerce` has **no direct CUE
import** (`go.mod` lists it `// indirect`) and **no `.cue` files**; it only contributes schema *strings*.

| File | Use |
| --- | --- |
| `framework/config/area.go` | builds the instance, evaluates, decodes, binds into Dingo |
| `framework/config/loader.go` | parses `.cue` files, `%%ENV%%` expansion, `CueDebug` dev API |
| `framework/config/cueast.go` | hand-rolled AST merge of multiple `.cue` files |
| `framework/config/configcmd.go` | `config` CLI dump |
| `framework/config/config.go` | `config.Map` / `config.Slice`, Go-side merge and number normalisation |

### 3.2 The load pipeline

`Area.loadConfig` (`area.go:227-301`) is a fixed 9-step pipeline in which **CUE runs last**, as a
validate-and-default pass over an already-merged Go map:

1. Fresh `build.Instance` per call (`area.go:228`).
2. `AddFile` in order: the `flamingo?: modules?: disabled?: [...string]` schema (`area.go:149`); every module's
   `CueConfig()` under the synthetic filename `pkgpath.TypeName` (`area.go:153-159`); a synthesized
   `flamingo: os: env: {...}` file for every process env var (`area.go:161-170`).
3. Deprecated `DefaultConfig()` maps merged (`area.go:175-188`).
4. `Configuration` reset to `Map{"area": <name>}` (`area.go:238`).
5. Go defaults, then all YAML, are `Add`ed — **YAML beats Go defaults** (`area.go:240-245`).
6. `OverrideConfigModule.OverrideConfig` may rewrite anything (`area.go:247-253`).
7. `checkLegacyConfig` copies legacy→new so the schema sees the new name (`area.go:255-257`).
8. The generated `purgeNil` file, then `AddSyntax` of the hand-merged user `.cue` AST (`area.go:268-275`).
9. `Build` → `Fill(Configuration)` → `Decode` → `Add` back over `Configuration` (`area.go:278-292`), then
   `checkLegacyConfig` again (new→legacy).

**CUE never removes anything.** Keys it hides simply keep whatever the Go merge produced. Module defaults reach
Dingo *only* through the step-9 decode-and-merge-back.

### 3.3 Load-bearing oddities

- **`purgeNil` (`area.go:259-266`)** emits `*null | _` for every nil leaf, because filling a Go `nil` produces top
  (`_`), which `Decode` rejects. **It is still required at v0.17.1** — verified: with no marker, filling
  `{"probe":{"x":null}}` fails on *both* versions. cuelang/cue#220 is not fixed. The mechanism is version-neutral;
  only its *interaction with schema defaults* changed (§5.3, S2/S3).
- **`cueast.go` implements last-file-wins override**, which CUE unification cannot express. When a name appears in
  both layers, structs merge recursively but a non-struct base value is dropped (`cueast.go:123`) and the incoming
  scalar wins (`:89`). Proven load-bearing: bypassing it fails Flamingo's own `TestLoad` on **both** versions with
  `conflicting values 12.0 and 22.0`. Behaviour is byte-identical across versions, and it needs **zero changes**
  for v0.17.1. It has known defects — an unchecked type assertion panic at `cueast.go:120`, and silent dropping of
  every non-`*ast.Ident`-labelled declaration in second and later files (`:73-91`).
- **Config is bound into Dingo by concrete Go type** (`area.go:331-340`), with `int`/`int64` bindings registered
  *only* when the value is `float64`. This is why decode types are a correctness issue.

## 4. Target state: CUE v0.17.1

### 4.1 Go API delta

`cue.Runtime` and `cue.Instance` still exist as types; their methods are gone.

| Current call | Replacement | Note |
| --- | --- | --- |
| `new(cue.Runtime).Build(bi)` (`area.go:278`) | `cuecontext.New().BuildInstance(bi)` | returns a `cue.Value`; **`.Err()` must be checked** — the classic porting trap |
| `inst.Fill(m)` (`area.go:283`) | `v.FillPath(cue.Path{}, m)` | the only correct mapping — see below |
| `inst.Value().Decode(&m)` (`area.go:289`) | `v.Decode(&m)` | reimplemented; changes number types (§4.3) |
| `inst.Lookup(path...)` (`loader.go:84`) | `v.LookupPath(...)`, `Syntax()` needs `cue.Final()` | dev API |
| `cueast_test.go:43`, `:91` | as `area.go:278` | 4th error, visible only under `go test` |

`build.Context`/`build.Instance`, `parser`, `format`, `errors` and the `ast` types used by `cueast.go` are intact.
**Flamingo needs no `cue.mod`** — it never calls `load.Instances`. Go toolchain is a non-issue (Flamingo is on
`go 1.25.8`; CUE v0.17.1 requires `go 1.25.0`).

**`FillPath` vs `Encode`+`Unify`.** Old `Fill` converted with `nilIsTop=true`, so Go `nil` became `*null | _`, not
`null`. `FillPath` hardcodes the same; `Context.Encode` defaults to `nilIsTop=false`. Since `config.Map` is full
of nils, the plausible-looking `Unify(ctx.Encode(m))` **breaks every nil config value**:

```
FillPath(cue.Path{}, m)                  -> {"host":"example.com","nilish":null}
Unify(ctx.Encode(m))                     -> Err: nilish: conflicting values string and null
Unify(ctx.Encode(m, cue.NilIsAny(true))) -> {"host":"example.com","nilish":null}
```

### 4.2 Language delta

Five constructs that v0.0.15 accepts are removed, all failing at parse time with messages that name neither the
construct nor the fix:

| Legacy (v0.0.15) | v0.17.1 | Error users will report |
| --- | --- | --- |
| `Foo :: {...}` definitions | `#Foo: {...}` | `expected operand, found ':'` |
| `core oauth: {...}` space-separated label paths | `core: oauth: {...}` | `expected label or ':', found 'IDENT' oauth` |
| `<Name>: {...}` templates | `[Name=string]: {...}` | `expected operand, found ':'` |
| `expr for x in y` trailing comprehensions | `for x in y { expr }` | varies |
| `/* ... */` block comments | `// ...` | `expected operand, found '/'` |

Two further changes are scope/semantics rather than syntax:

- **Cross-file references require a package clause.** A file with no `package` clause no longer contributes its
  top-level identifiers to the instance scope. Flamingo adds one anonymous file per module, so a schema
  referencing another module's path fails: `reference "flamingo" not found`. Required since **v0.3.1** — broken
  long before v0.17.
- **Optional fields cannot be referenced.** `enabled?: bool` plus `if flamingo.prefixrouter.rootRedirectHandler.enabled {...}`
  is now `cannot reference optional field: enabled`, killing `framework/prefixrouter`'s own schema
  (`module.go:58-64`) at **build**, not parse.

**No dual-valid dialect exists.** `#Foo:`, `let`, `a!:`, `@tag()` and dynamic labels are parse errors on v0.0.15;
`::` is a parse error on v0.17.1. Per-file migration is atomic by construction. Definition *closedness* is
identical in both versions, so the `::`→`#` rewrite is semantics-preserving.

### 4.3 Decode and evaluator delta

- **Numbers.** v0.0.15's `Decode` was `MarshalJSON` + `json.Unmarshal`, so every number arrived as `float64`.
  v0.17.1 uses a reflective decoder: integers → `int64` (or `*big.Int`), floats → `float64`, bytes → `[]byte`.
  This is a named, version-gated CUE experiment (`DecodeInt64`: preview v0.11, default v0.12, stable v0.13) and
  **cannot be switched off**.
- **Top-level non-concrete values decode to `nil` instead of erroring** (`cue/decode.go:159-166` short-circuits
  nullable Go kinds when the value is not concrete). Nested fields still error on both versions.
- **Conflicting module defaults for one top-level key** silently resolve where they used to be a hard boot error.
- **`*null | _` no longer destroys a schema default** (see S2/S3 below).

### 4.4 What does not exist

- **No language compatibility mode.** Declaring an older language version — down to `"v0.0.15"` — re-enables no
  removed syntax; `token.ISA` and `ast.TemplateLabel` are gone from the tree. Declaring an old version only makes
  the parser *stricter*.
- **No evaluator fallback.** `cuecontext.EvalV2` does not exist at v0.17.1 and `CUE_EXPERIMENT=evalv3=0` is
  rejected. Old-vs-new comparison needs two module versions.
- **No useful stepping stone.** Version bisects show no intermediate hop isolates a hazard class: the cross-file
  scope break lands at v0.3.1, silent default resolution at v0.3.0-beta.5, the anonymous-file exclusion at v0.4.0,
  and the decode change at v0.11–v0.13. Any intermediate pin carries some hazards and not others while adding a
  release. **Go in one hop.**

Two "compatibility" ideas were evaluated and **both rejected**: a hidden-field bridge (`_http` alongside `#http`)
does not deliver compatibility — an unmigrated config referencing `core.auth.http` still fails — costs customers
two edits instead of one, and silently disables closed-struct validation on exactly the config a definition exists
to constrain. A two-release deprecation window cannot work either, because no file is valid on both versions.

## 5. Breakage analysis

### 5.1 Flamingo's Go code

4 compile errors, all in `framework/config` (§4.1). Loud, trivial. Not the problem.

### 5.2 Flamingo's own schemas — 13 of 28 (46%)

Measured by extracting every `CueConfig()` body and **building** (not merely parsing) each on v0.17.1:

| Cause | Schemas | Detected at |
| --- | --- | --- |
| `::` definitions | 8 | parse |
| space-separated label paths | 3 | parse |
| cross-file reference, no package clause (`core/auth`) | 1 | build |
| optional-field reference (`framework/prefixrouter`) | 1 | build |
| **Total failing** | **13 of 28** (10 of 21 flamingo, 3 of 7 commerce) | |

A legacy-syntax lint found **18 legacy constructs across 8 files** in flamingo and **4** in flamingo-commerce
(`category/module.go:98,101`, `checkout/module.go:107`, `product/module.go:75`).

**A parse-only gate is the wrong success condition** — it passes prefixrouter and both cross-file blockers. The
CI gate must build and validate each schema individually *and* in the full combination.

`prefixrouter` is opt-in, but `core/healthcheck/module.go:10` imports the package and flamingo-commerce's
integration harness registers it, so it breaks the commerce test suite. Its fix (`enabled: bool | *false`) makes
the key concrete, which **adds** `flamingo.prefixrouter.rootRedirectHandler.enabled` to decoded config and creates
a new Dingo binding — a real config-surface change that must be allowlisted and documented.

**After migration, all 28 parse and build, and the combined configuration decodes byte-identically** — same
values, same leaf Go types.

### 5.3 Silent semantic changes — read this section first

These are the migration's real risk: config that still loads and now means something different.

| # | Change | v0.0.15 | v0.17.1 | Severity |
| --- | --- | --- | --- | --- |
| **S1** | An unparseable `.cue` file is silently discarded and the app boots on schema defaults | file dropped, app boots (pre-existing) | same — but now for six years' worth of previously valid syntax | **BLOCKER** |
| **S2** | Unset `%%ENV:VAR%%` on a key whose schema has a default | **hard boot failure** | **boots silently on the default** | **HIGH (security)** |
| **S3** | `key: ~` on a schema key with a **non-empty** marked default | `[]` | the default | MEDIUM |
| **S4** | Top-level non-concrete value decodes to `nil`, and `Get()` returns **ok=true** | hard load error | `Load` returns nil, key present and nil | HIGH |
| **S5** | Numeric elements **inside** `config.Slice` flip `float64`→`int64` | `float64` | `int64`; `element.(float64)` → ok=false | LOW |

**S1 decides the plan's shape.** Reproduced end to end: a project pre-migrates `::`→`#` while still on v3, the
file no longer parses on v0.0.15, `loadLogged` discards it, `config.Load` returns **nil**, the app boots, and keys
silently revert to schema defaults or vanish. Making `.cue` load failures loud is therefore a **hard gate on the
entire migration**, not a nice-to-have.

**S2 is the one to lead the user guide with.** The documented unquoted `%%ENV:VAR%%` pattern (`loader.go:34`,
`:196-205`, `docs/2. Framework Modules/Configuration.md:28`) yields `nil` when the variable is unset. `purgeNil`
then emits `*null | _`, and `(string | *"d") & (*null | _)` resolves to an incomplete `string` on v0.0.15 (hard
error) and to `"d"` on v0.17.1. Concretely: `flamingo.session.secret: %%ENV:SESSION_SECRET%%` with the variable
unset stops the boot today and **silently starts the app on the hardcoded default `"flamingosecret"`** after the
upgrade. Hand-written `~` has **zero occurrences** in either repo; the env path is everywhere.

**S3 is narrower than it first appeared.** It fires *only* when the schema carries a non-empty marked default
(`[...T] | *[a,b]`). `| *[]`, bare `[...T]`, `[...{…}]` and unschema'd keys decode identically on both versions.
The true in-repo population is **two keys**: `core/oauth/module.go:89` (`scopes`) and commerce
`product/module.go:97`. Earlier claims naming `core.locale.*`, `flamingo.opencensus.*` and `core.auth.web.broker`
were checked and are **wrong**.

**S4 does not change any currently-working config.** A config that loads on v0.0.15 contains no non-concrete
value, so it decodes identically; what changes is the failure mode of configs that were *already broken*. All 28
schemas are namespaced under `core:`/`flamingo:`/`commerce:`, so framework fields sit at depth ≥2 and stay loud.
Exposure is top-level keys in user `.cue` files. The aggravating detail: the nil lands in `config.Map` with
`Get()` returning **ok=true**, defeating `if v, ok := cfg.Get(k); ok` presence checks.

**S5's blast radius in the shipped ecosystem is zero.** Every list schema in both repos is `[...string]` or
`[...{string,string}]`; there are no numeric list schemas and no numeric list literals in any `.cue` or YAML, and
every real `config.Slice` consumer is string-asserting or goes through `MapInto`'s JSON round-trip. Scalars are
immune because `config.Map.Add` (`config.go:85-86`) normalises them and recurses into nested `Map`s; only `Slice`
interiors survive, since `Add`/`Flat` treat `Slice` as an opaque leaf. Exposure is hypothetical customer code —
real and silent, worth a remedy, not a blocker.

### 5.4 User configuration

| Change | Loud? | Notes |
| --- | --- | --- |
| Removed syntax in a user `.cue` | Loud in principle — **silently dropped in practice** (S1) | The reason S1 is the gate |
| `::`→`#` renames the *reference path*: `core.auth.http` → `core.auth.#http` | Loud, but **two different failure modes** | If the parent is an open struct: `cannot convert non-concrete value` at decode. If the parent is itself a definition (closed): `undefined field` at build. |
| Nested definitions need **both** segments rewritten | Loud | `core.auth.fake.UserConfig` → `core.auth.#fake.#UserConfig`. Rewriting only the first segment yields `undefined field: UserConfig`. |
| S2–S5 | Silent | §5.3 |

**Scope of the rename cascade:** **5 customer-reachable dotted renames** under `core.auth.` — `oauth2Config`,
`oidc`, `http`, `fake`, `fake.UserConfig` — plus **2 example-only bare identifiers** (`StaticAuthBroker`,
`customOidcBroker` in `core/auth/example/custom/`) that a codemod should report and refuse to touch.
**flamingo-commerce does not cascade at all**: all four of its `::` definitions are referenced only inside their
own `CueConfig()` strings, so its exposure is fixed by its own maintainers.

**Not affected: the documented YAML contract.** `loader.go:181-205` expands `%%ENV:%%` and then
`yaml.Unmarshal`s; YAML values never reach the CUE parser. Full flattened config dumps are identical on both
versions. Exactly one `cue` fenced example exists in all documentation (`core/auth/fake/Readme.md:11-37`) and it
breaks verbatim.

### 5.5 Error quality

Mixed, not uniformly worse — but Flamingo makes it worse than it needs to be. `cueError` (`area.go:139-144`)
surfaces only `err.Error()`, and v0.17.1 truncates:

```
old: cue: marshal error at path myapp.ports: empty disjunction: conflicting values int and 9090 ...
new: myapp.ports: 2 errors in empty disjunction: (and 2 more errors)
```

24 of 28 schemas use default-marked disjunctions, so this hits nearly every config error. `errors.Details(err, nil)`
recovers the full text **and exists in both versions**; `Value.Validate(cue.Concrete(false))` enumerates the whole
tree instead of stopping at the first path.

### 5.6 Performance and dependency surface

Runtime cost is not a reason to fear this migration:

| Scenario | v0.0.15 | v0.17.1 | Delta |
| --- | --- | --- | --- |
| Single area, 9 real modules | 2.8 ms | 3.1 ms | +11% |
| 50 child areas | 128 ms | 168 ms | +31% |
| Peak RSS (single / 50 areas) | 23.5 / 47.3 MB | 25.5 / 60.3 MB | +9% / +27% |

Complexity class is unchanged. What every adopter sees first, however, is the dependency surface:

| Measure | v0.0.15 | v0.17.1 |
| --- | --- | --- |
| `examples/hello-world` binary | 22,822,713 B | 28,386,833 B (**+5.56 MB, +24.4%**) |
| cuelang packages linked into `framework/config` | 13 | 96 (7.4×) |

The module graph also gains `github.com/tetratelabs/wazero` (a WebAssembly runtime), `cuelabs.dev/go/oci/ociregistry`,
`github.com/coder/websocket` and the OCI image-spec packages. **These are not linked into the binary** — verified
with `go list -deps` on the ported tree. State this in the release notes: otherwise the first enterprise security
review that reads the new `go.mod` blocks the upgrade on "why does our web framework vendor a WASM runtime?".

## 6. Recommended strategy

**Shape:** instrument on v3, predict before the bump, codemod carefully, then one small v4 change. The `.cue`
edits land in a **different release** from the `go.mod` bump. Indicative sizing ~14 weeks across four releases.

### Phase 0 — v3.x patch: stop lying about broken config (~2 weeks)

Ships on CUE v0.0.15. Nothing here is a CUE upgrade, so nothing here is blocked by the standing v3 veto, and
every item is independently valuable.

| ID | Work |
| --- | --- |
| WI-0.1 | **Make `.cue` parse errors fatal.** Change `loadLogged` (`loader.go:130-137`) to return errors and propagate at the **four** `loadCueFile` call sites — `loader.go:101` (the `CONTEXTFILE` path), `:141`, `:148`, `:152`. Omitting `:101` ships a strict mode with a `CONTEXTFILE`-shaped hole in exactly the path that hides production config. |
| WI-0.2 | Gate WI-0.1 behind an env switch (strict by default, opt-out to WARN) for two minors. WI-0.1 correctly turns a years-old silent breakage into a failed deploy — which is still an outage the team caused. |
| WI-0.3 | Comma-ok the unchecked type assertion panic at `cueast.go:120`. |
| WI-0.4 | WARN on every declaration silently dropped at `cueast.go:73-91`. This is also the cheapest way to discover what customers actually have in their `.cue` files. |
| WI-0.5 | Rewrite `cueError` (`area.go:139-144`) to `errors.As` + `errors.Details`. |
| WI-0.6 | `flamingo config snapshot` — one TSV line per `Flat()` key: key, concrete Go type, JSON value. The type column **must recurse into `config.Slice` elements** or the decode delta is invisible; env redaction must be **structural** (drop the `flamingo.os.env` subtree). |
| WI-0.7 | `flamingo config lint` — report nil keys whose schema has a non-empty default (S3), grep for the five removed constructs, and print the `CONTEXT` set and the files actually merged. |
| WI-0.8 | Redact `flamingo.os.env` from `flamingo config` behind an explicit `--show-env` flag. |
| WI-0.9 | **Per-module schema build probe in CI** — every `CueConfig()` is `AddFile`d, built and validated, individually *and* in the full combination. This is the fix for §1.1; parse success is not sufficient. |

**Exit gate G0.** Suite green with the fatal path; `config lint` reports zero CHANGED keys across flamingo's
testdata and commerce's fixtures; a `.cue` file with removed syntax fails the boot with a positioned error. Ship
with a release note titled for what it does — *"this release will tell you your config is broken"*.

**Collection window (3–4 weeks) — a gate, not an overlap.** Ask projects to commit a config snapshot and send back
`config lint` output. This produces the number every prior estimate lacked: how many projects load a `.cue` file
at all. **Branch on it:** if few projects carry `.cue` files, ship the codemod (Phase 2) and skip the doctor
(Phase 1) entirely. As written, Phases 1–2 are ~7 weeks committed against a population nobody has sized — and the
two strongest pieces of evidence (YAML-only projects are unaffected; flamingo-commerce, the designated
"config in the wild" proxy, has zero `.cue` files) both point at a small number.

### Phase 1 — the predictive doctor (~4 weeks, gated on the collection window)

A tool that tells a project what *would* change, before it touches `go.mod`. Two viable architectures (§7).
Deliverables: bundle capture at the exact `Fill` call site, a Go-typed side channel captured at `Flat()`, a
differ classifying **CRITICAL** (value changed / key disappeared), **HIGH** (same value, different Go type) and
**INFO** (key appeared), and a golden corpus with `TestGoldenConfigDiff` gated on an `ALLOWED.md` allowlist.

**Gate G1.** The doctor must be honest about its limit: for a project carrying 2019 syntax the new side cannot
parse, so the report degrades to a parse error and value-level diffs are gated on Phase 2. It must say so in its
own output rather than reading as a clean bill of health.

### Phase 2 — `flamingo cue-migrate` (~3 weeks)

Pin the codemod to **`cuelang.org/go v0.4.3`**, the newest version that parses *both* dialects. Three
non-negotiable rules, each learned by getting it wrong:

- **Per-build-unit symbol scoping.** The obvious global name-keyed implementation silently deleted
  `commerce.sourcing.fake.enable` during testing. Resolve references through the AST with scope awareness, never
  by textual name.
- **Longest-prefix, multi-segment definition paths.** `core.auth.fake.UserConfig` must become
  `core.auth.#fake.#UserConfig`; rewriting one segment produces `undefined field`.
- **Never gate on `cue fix`'s exit code.** Modern `cue fix` exits 0 and changes nothing on files it cannot parse.
  The v0.2.x `cue fix` is the only one that rewrites `::`, but it emits `@tmpNoExportNewDef` bridge fields that
  leak **new config keys** and must be stripped — which is why a purpose-built codemod on v0.4.3 is preferred.
- **Ship a `--reverse` mode** so a project can get back to v3-parseable config (§8, rollback).

Publish the old→new symbol table as data so the tool can rewrite customer files it has never seen — split into
the 5 dotted renames and the 2 example-only bare identifiers it will refuse to touch. Precedent for shipping
migration scripts exists: `docs/updates/v3/renameimports.sh`.

### Phase 3 — dogfood (~2 weeks)

Run the codemod over both repos. **Rule: no hand-edits** — if something needs one, fix the tool and re-run.
Measured scope: 14 units change. Exit criteria: both repos migrated with zero hand-edits, suites green on
v0.17.1, doctor clean against the Phase 1 baselines.

*Already proven:* for the real auth example, v0.0.15 evaluating the **original** syntax and v0.17.1 evaluating the
**migrated** syntax produce byte-identical decoded JSON and byte-identical leaf Go types.

### Phase 4 — v4.0.0: the bump (~3 weeks)

| ID | Work |
| --- | --- |
| WI-4.1 | `go.mod` → `cuelang.org/go v0.17.1`; `go mod tidy` |
| WI-4.2 | `area.go:278` → `cuecontext.New().BuildInstance(...)`, field `*cue.Instance` → `cue.Value`, check `.Err()` immediately |
| WI-4.3 | `area.go:283` → `FillPath(cue.Path{}, ...)` + `.Err()`. **Not** `ctx.Encode` |
| WI-4.4 | `loader.go:84` → `LookupPath`, pass `cue.Final()` to `Syntax()` |
| WI-4.5 | **Package clause** on every generated `AddFile` (`area.go:149,155,168,268`) and injected into the AddSyntax'd user AST. User `.cue` files need no change — an anonymous file can still read identifiers a packaged file put in scope. **Must specify behaviour for a user file that declares its own package** (strip / adopt / diagnose): it already fails on v0.0.15, but an unconditional prefix makes Flamingo the cause of a failure plain v0.17.1 would not have. |
| WI-4.6 | **`Validate(cue.Concrete(true))` before decode**, restoring the fail-fast lost to S4. Measured zero false positives across the whole suite and a 328-key real config; verified compatible with `purgeNil` (a `*null | _` resolving to concrete `null` passes). Wrap it to re-attach a source position, and ship behind a flag for one release. |
| WI-4.7 | `area.go:289` `Decode(&m)` → `MarshalJSON()` + `json.Unmarshal(&m)`. Reproduces v0.0.15 numerics byte-for-byte including `*big.Int`, neutralising S5 **and** the `Slice`/scalar inconsistency in one change. Pairs with WI-4.6, which removes `MarshalJSON`'s one failure mode. Zero new test failures. |
| WI-4.8 | Fix the Dingo binding loop (`area.go:331-340`) to bind `float64`, `int64` and `int` for **any** integral number, not only inside `v.(float64)`. This is the compatibility hinge for existing `inject:"config:…"` consumers. Comma-ok the unchecked assertions at `:342-346`. |
| WI-4.9 | **Keep `cueast.go`.** Port its test fixtures from `::` to `#` and add a package clause; all 13 assertions then pass unchanged. Do **not** rewrite the merge on the critical path — deleting it fails Flamingo's own testdata. |
| WI-4.10 | Rewrite `prefixrouter`'s `enabled?: bool` → `enabled: bool | *false`, and allowlist the new config key it introduces. |
| WI-4.11 | Cash the free API deletions: `DefaultConfigModule` (0 implementors in flamingo, 1 test helper in commerce), `OverrideConfigModule` (0 implementors), `LoadConfigFile`, `GetFlatContexts`, `LoadedConfig`. **Keep** `flamingoLegacyConfigAlias` (14 + 5 implementors) but remove its two `log.Fatal` calls from library code. |

**Exit gate G4.** Both repos green. Doctor produces an empty diff against every reference app. Headline
acceptance test, wired into CI: *a v3.x project that reported an empty doctor diff boots on v4.0.0 with a
byte-identical `flamingo config` dump.* Do not ship the `OverrideConfigModule`/`DefaultConfigModule` removals
without a release-note call for implementors first — "0 implementors" covers two framework repos, not customer
projects, and an unbounded post-merge config hook is exactly the escape hatch a large project builds.

### Phase 5 — post-GA

Keep `TestGoldenConfigDiff` a permanent required gate and pin the version bisects as CI tests, so the next CUE
bump is caught by CI rather than by a customer. Defer any `cueast.go` replacement to v4.1, gated on the WI-0.4
warning telemetry.

## 7. Validation approach

Because no file is valid on both versions, "it still parses" proves nothing about meaning. The safety net is a
**cross-version diff of values *and* the concrete Go type of every leaf**. Two architectures work:

**(a) Bundle + side-car.** Export a version-neutral bundle — every source handed to `AddFile`, captured as raw
strings at the call sites, plus the Fill map snapshotted at the exact `Fill` call site (`area.go:283`; capturing
after `loadConfig` returns produced 31 false-positive rows in the prototype). Replay it through a separate binary
pinned to the other CUE version. Nothing is parsed at export time, so **a bundle exports fine from a project whose
config no longer parses** — exactly the project that needs checking. Cost: six bundle-fidelity hazards, each
learned by getting it wrong.

**(b) In-process fork.** The claim that two `cuelang.org/go` versions cannot link into one binary is **false**, and
was demonstrated: MVS only collapses versions of the *same module path*, so vendoring v0.0.15 under a rewritten
path (`cp` the module, `sed` the import path across 155 files, one `replace` directive) lets one binary import
both and diff in-process. Cost: a mechanically-rewritten copy to carry. Benefit: **all six bundle-fidelity hazards
disappear, because there is no bundle.**

Pick per Phase 1 sizing; (b) is materially cheaper than the plan that assumed (a) was forced.

Proven results on real material (bundle architecture):

| Corpus | Keys (old/new) | Diffs |
| --- | --- | --- |
| Flamingo `testdata/valid` | 148 / 148 | **0** |
| flamingo-commerce, all 7 schemas migrated | 192 / 192 | **0** |
| `core/auth/example/config` (7 modules, 4 with definitions, 2 layered `.cue`) | 279 / 279 | **0** |

And proven **not** to be a no-op: with number normalisation off it reports the exact drift (5 diffs); against a
deliberately mis-migrated auth schema it reports **13 diffs** pinpointed at nested list-element keys a JSON
eyeball-diff would miss.

> Carried forward from review: the harness prototype's tests printed results instead of asserting them, and
> embedded absolute paths. Before this ships, every harness test must turn **red** when the feature it covers is
> reverted — this was mutation-tested and three shipped features were found to have no enforcing assertion.

## 8. User migration guide (draft)

**If your project only uses YAML** — `config.yml`, `config_<context>.yml`, `routes.yml`, `%%ENV%%` interpolation,
dotted or nested keys — **the syntax cutover does not affect you.** Verified byte-identical. Read item 1 anyway.

1. **Unset `%%ENV:VAR%%` no longer refuses to boot.** On v3, `flamingo.session.secret: %%ENV:SESSION_SECRET%%`
   with the variable unset was a hard boot failure. On v4 the same config **boots silently on the schema
   default** — for that key, the hardcoded `"flamingosecret"`. Audit every `%%ENV:%%` reference on a key whose
   schema declares a default, and make required secrets fail loudly in your own deployment checks.
2. Run `flamingo config lint` and `flamingo config snapshot` **on v3, before changing anything**, and commit the
   snapshot.
3. Run `flamingo cue-migrate` for the syntax rewrite. Never hand-run a modern `cue fix` and trust its exit code.
4. **Do not** replace `::` with `:` — that turns a schema-only definition into a required concrete config field,
   which either errors at decode or silently injects new keys.
5. Update references to framework definitions using the published table (5 dotted renames under `core.auth.`).
   Nested definitions need **both** segments: `core.auth.fake.UserConfig` → `core.auth.#fake.#UserConfig`.
6. Run the doctor and require an empty diff before bumping.
7. Audit `key: ~` overrides of keys whose schema has a non-empty default (S3), and `.(float64)` assertions on
   config list elements (S5).

Errors you may see:

| Error | Cause | Fix |
| --- | --- | --- |
| `expected operand, found ':'` | `::` definition or `<Name>` template | `#Name:` / `[Name=string]:` |
| `expected label or ':', found 'IDENT' x` | space-separated label path | insert colons |
| `reference "flamingo" not found` | cross-file reference | framework-side; upgrade Flamingo |
| `cannot reference optional field` | `if` guard over an optional field | give the field a concrete default |
| `undefined field: X` at build | nested definition, only one segment renamed | rename both segments |
| `cannot convert non-concrete value` at decode | unmigrated reference under an open struct | rename the reference |
| config silently missing | pre-WI-0.1 Flamingo swallowed a parse error | upgrade to a release with WI-0.1 |

## 9. Risks and open questions

| Risk | Mitigation |
| --- | --- |
| A project's config silently loses values (S1) | WI-0.1 ships a release *before* the cutover; this is the plan's hard gate |
| A production app boots on a default session secret (S2) | Lead the guide with it; `config lint` flags it; consider failing loudly on a defaulted secret key |
| The codemod corrupts config via name collisions | Per-build-unit scoping, AST-based, gated on a decoded-key-set diff — which caught exactly this during the investigation |
| **No rollback** | The cutover is atomic by construction: `.cue` edits shipped in Phase 3 do not parse on v0.0.15, so a project cannot downgrade the framework without reverting its config. `cue-migrate --reverse` is the mitigation and must ship with the forward mode |
| The migration stalls like 2021 | The 2021 attempt had no inventory, no harness and no phased plan. Phase 0 delivers value even if v4 slips |
| Phases sized against an unknown population | Make the collection window a gate (§6), not an overlap |
| Enterprise security review blocks on the new module graph | Release note states the WASM/OCI dependencies are not linked (§5.6) |

**Open questions, each with the experiment that settles it:**

- How many projects load a `.cue` file at all? *Ship Phase 0 and read the collection window.* This decides whether
  Phase 1 happens.
- Are there user `.cue` files that declare their own package clause? *The linter should flag them; WI-4.5 depends
  on the answer.*
- Should numbers stay `float64` forever, or should v4 adopt native types with a documented break? *Currently
  recommended: keep `float64` via WI-4.7, because the alternative's failure mode is a silent `,ok` type-assertion
  failure rather than a loud error.*
- Should `purgeNil` survive? *It is still required at v0.17.1; revisit only if cuelang/cue#220 is fixed.*

## 10. Appendix

### Reproducing the analysis

```bash
# Latest stable CUE
go list -m -versions cuelang.org/go | tr ' ' '\n' | tail -5

# The Go-side breakage (3 errors; a 4th appears under `go test`)
cp -a <flamingo> /tmp/probe && cd /tmp/probe
go mod edit -require=cuelang.org/go@v0.17.1 && go mod tidy && go build ./...

# BUILD (not just parse) every extracted schema on v0.17.1 — this is what finds prefixrouter
for f in schemas/*.cue; do <probe-new> "$f"; done

# Two CUE versions in one binary, for an in-process differ
cp -a $(go env GOMODCACHE)/cuelang.org/go@v0.0.15/. forkcue/ && chmod -R u+w forkcue
cd forkcue && grep -rl 'cuelang.org/go' . --include=*.go \
  | xargs sed -i 's|cuelang\.org/go|oldcue.local/go|g'
sed -i 's|^module cuelang.org/go|module oldcue.local/go|' go.mod && go build ./cue/...

# Cost of the bump, in the units adopters measure
go build -o bin-old ./examples/hello-world     # 22,822,713 B
go list -deps ./framework/config | grep -c cuelang   # 13 -> 96
```

A probe harness is two tiny modules, one per CUE version, that `AddFile` the same sources into a `build.Instance`,
decode, and print the concrete Go type of every leaf. **Type output is mandatory** — JSON hides `int64`/`float64`.

### Removed-syntax quick table

| Legacy | Modern |
| --- | --- |
| `Foo :: {...}` | `#Foo: {...}` |
| `a b c: v` | `a: b: c: v` |
| `<Name>: {...}` | `[Name=string]: {...}` |
| `expr for x in y` | `for x in y { expr }` |
| `/* ... */` | `// ...` |

### Key source references

- `framework/config/area.go:139` (`cueError`), `:149`, `:161-170`, `:227-301`, `:259-266` (purgeNil),
  `:331-340` (typed Dingo bindings), `:342-346` (unchecked assertions)
- `framework/config/loader.go:34` + `:196-205` (`%%ENV%%`), `:84` (`CueDebug`), `:101,141,148,152`
  (`loadCueFile` call sites), `:130-137` (swallowed loader errors)
- `framework/config/config.go:36-41` (`Slice` conversion), `:85-86` (number normalisation), `:108` (`Flat`)
- `framework/config/cueast.go:73-91` (dropped declarations), `:120` (unchecked assertion), `:123` (override)
- `framework/prefixrouter/module.go:58-64` (optional-field reference)
- Upstream branch `201-update-cue`, commit `d616ac4` (July 2021, abandoned v0.4.0 attempt)
