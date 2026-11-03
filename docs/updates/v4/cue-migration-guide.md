# Migrating configuration to Flamingo v4

> **Draft.** This guide follows the design in [cue-migration-strategy.md](cue-migration-strategy.md) and will be
> finalised with the v4.0.0 release. Commands, flags and keys marked *(planned)* do not exist yet and may change before
> release.

| Version | What it is |
| --- | --- |
| v3.18 *(planned)* | Adds `config snapshot` only. It changes no behaviour |
| v4.0.0-alpha *(planned)* | The first v4 pre-release, with `/v4` import paths, the legacy engine and engine selection. Later alpha releases add the modern engine as an opt-in, `config compat` and `config cue-migrate` |
| v4.0.0-beta *(planned)* | Adds the dingo v2 signature changes, Go 1.27 and pre-releases of the ecosystem modules |
| v4.0.0 *(planned)* | The first stable v4 release, once its exit criteria are met |
| v5 *(planned)* | The modern engine becomes the default. The legacy engine stays until v6 at the earliest |

This guide covers configuration only. For other API changes, see the v4 release notes.

## Why

Flamingo validates your configuration, and fills in defaults, with CUE schemas that each module provides. Flamingo v3
uses CUE v0.0.15 from 2019. Flamingo v4 moves to the current CUE, v0.17.1, so that the framework runs on a maintained
CUE and modules can use current syntax. The two versions differ in syntax and in a few results. v4 hides these
differences, so you do not have to change your configuration.

## What v4 means for your configuration

You have nothing to convert. A configuration that boots on v3 boots on v4 with the same keys, values and Go types. With
the legacy engine it also gives the same errors. This holds for YAML files, `.cue` files, `CONTEXTFILE` entries and the
schemas of third-party modules.

v4 accepts the legacy CUE syntax for the whole v4 line. Each use gives a deprecation warning with file and line. Where
CUE v0.17 would evaluate your configuration differently, v4 reproduces the v3 result and reports what would change.

v4 has two config engines. They produce the same `config.Map`, so everything after the load is unchanged.

- The legacy engine is the v3 code on a bundled, renamed copy of CUE v0.0.15. It gives the v3 result, quirks included.
- The modern engine runs on CUE v0.17.1. A translator converts legacy files and schemas in memory, and compat shims
  reproduce v3's results.

The default, `auto`, picks the legacy engine unless a file or schema can only be read by the modern engine.

The maintainers keep v3 baselines for 11 production applications, with 39 deployed contexts and 10 CI contexts. Before
v4.0.0, v4 must give the same result on all of them. The legacy engine must match exactly. The modern engine may differ
only in the documented cases (see [Choosing the engine](#choosing-the-engine)). You should still check your own
configuration. Steps 1, 3 and 4 do this.

## Is your app affected?

Affected means that you will see warnings, not that anything breaks. Find out whether your app has `.cue`
configuration or schemas of its own:

```sh
find config -name '*.cue'        # config directories of all areas (or the directory passed to flamingo.ConfigDir)
# externally mounted files: the loader strips each CONTEXTFILE entry's extension and also loads <entry>.cue
echo "$CONTEXTFILE" | tr ':' '\n' | sed 's/\.[^./]*$//' | while read -r f; do
  [ -n "$f" ] && [ -f "$f.cue" ] && echo "$f.cue"
done                             # run this with each deployment's CONTEXTFILE
grep -rl --include='*.go' --exclude-dir=vendor 'CueConfig()' .   # your own modules with embedded schemas
```

- If you have `.cue` files, you may see syntax warnings that name them. Follow the steps below.
- If your own modules have `CueConfig()`, read [For module authors](#for-module-authors).
- If you have YAML only, your files give no syntax warnings. A module you use can warn about its own schema, with a
  position like `<import path>.<Type>:line:col`. Tell its maintainers. Your app needs no change.
- Any app can also see warnings for a `.cue` file that cannot be parsed, for a `CONTEXTFILE` entry that matches no file
  and for a `flamingo.me/flamingo/v3/…` path in `flamingo.modules.disabled`. v3 ignored the first two silently. See
  [Warnings and how to fix them](#warnings-and-how-to-fix-them).

The Go API changes in v4 are separate: every Flamingo-based dependency needs a v4-compatible release.

## Steps for application developers

### 1. Upgrade to v3.18 and take a baseline snapshot of every deployed context

v3.18 only adds `config snapshot`, so you can roll it out like any minor release.

List every context you deploy. A context is one combination of `CONTEXT` value, `CONTEXTFILE` entries, child area
(`--flamingo-context`), environment variables and flags. Run the command once per context. `go run .` stands for
however you start your application.

```sh
CONTEXT=production go run . config snapshot > snapshot-v3-production.txt   # (planned)
```

Run it in the deployment, for example as a one-off job, and run the v4 build there the same way before rollout. Or set
the same environment variables locally. Use the same placeholder values for secrets in both runs. Otherwise, failures
that depend on production values show up only in the first rollout stage.

For each area, the snapshot has one line per key with the Go type and the SHA-256 of the canonical JSON value. It also
lists the modules that remain after `flamingo.modules.disabled` is applied, and the entries that matched no module. v4
writes the same format.

Keep snapshots private. Low-entropy values such as booleans and ports can be guessed from their hash. Do not use
`config` dumps for this. They print every environment variable and secret in clear text.

### 2. Move to v4 and the `/v4` import paths

```sh
find . -name '*.go' -not -path './vendor/*' \
  -exec perl -pi -e 's#flamingo\.me/flamingo/v3#flamingo.me/flamingo/v4#g' {} +
go get flamingo.me/flamingo/v4@v4.0.0
go get <each Flamingo-based dependency>@<its v4-compatible release>
go mod tidy && go build ./... && go test ./...   # with vendoring: go mod tidy && go mod vendor && go build ./...
go list -deps ./... | grep '^flamingo.me/flamingo/v3/'   # must print nothing
grep -rn 'flamingo.me/flamingo/v3' --exclude-dir=vendor --exclude-dir=.git --exclude=go.sum . | grep -v '\.go:'
```

- The last command lists build scripts and tool configs that still name v3. Update them, especially
  `-ldflags "-X flamingo.me/flamingo/v3/framework/flamingo.appVersion=…"`. The linker silently ignores an `-X` for a
  symbol that no longer exists, and `version` then prints `develop`.
- Each line that the `go list` command prints is a dependency that still imports v3. One that uses the web or config
  packages fails `go build`. One that uses only `framework/flamingo` builds, and v4 silently ignores its bindings.
  Upgrade it.
- Leave the entries for Flamingo's own modules in `flamingo.modules.disabled` on their v3 paths. v4 accepts the
  `flamingo.me/flamingo/v3/…` paths. v3 still matches them, so a rollback stays possible. For a dependency whose
  v4-compatible release has a new module path, list the old and the new path.
- v3 panics on an entry that matches no module in an app that uses `flamingo.WithRoutes`. Before a rollback to v3,
  remove the entries for new module paths.

Your config files stay as they are.

### 3. Compare snapshots

Take the v4 snapshot of every context the same way as its baseline, with the same `CONTEXT`, environment variables
and flags.

```sh
CONTEXT=production go run . config snapshot > snapshot-v4-production.txt   # (planned)
diff snapshot-v3-production.txt snapshot-v4-production.txt
```

The diff must be empty for every context. The only accepted lines are changes that a dependency lists in its release
notes. If the module list shows Flamingo's own modules with a `/v3` path in one snapshot and a `/v4` path in the other,
that is accepted too. If the baseline is older than the commit that moves to v4, take it again from that commit's
parent. Then unrelated config changes do not show up in the diff.

| Diff shows | Usual cause |
| --- | --- |
| Any difference with the legacy engine | A bug. Report it with the two lines |
| A different set of disabled modules | An entry in `flamingo.modules.disabled` for a dependency whose module path changed |
| A difference only with `engine: modern` | A known difference of the modern engine. `config compat` names the cause |
| Many keys missing or different | The two runs used different files, environment variables or flags |

### 4. Add `config compat` to CI

`config compat` *(planned)* runs both engines on one context. It reports every key that would differ, with its cause,
and exits non-zero when any key differs.

```sh
CONTEXT=production go run . config compat   # (planned)
```

Run it in CI for every context that CI can reproduce, with placeholder values for secrets. A difference breaks nothing
on v4. It shows what the modern engine would change before it becomes the default in v5.

### 5. Optional: convert your files with `config cue-migrate`

`config cue-migrate` *(planned)* rewrites `.cue` files to modern syntax. You do not need it. Without it, the warnings
stay and the app keeps running.

```sh
go run . config cue-migrate                           # (planned) dry run: prints a diff, writes nothing
go run . config cue-migrate --write                   # (planned) applies the diff
go run . config cue-migrate --write --schemas out/    # (planned) --schemas only if your own modules have CueConfig()
```

Commit your work first, so that you can review and revert the result. Work on copies of externally mounted files.

- It rewrites definitions and their references, label paths, comments, comprehensions, aliases, infix `div` and
  backquoted labels.
- It reads legacy input with the bundled CUE v0.0.15 parser and resolves references against your app's module schemas.
- It refuses unsafe cases and names the position. It also verifies every rewrite: the modern engine on the result must
  give the same configuration as the legacy engine on the original. If anything differs, nothing is written.
- It does not convert list arithmetic. Write `list.Concat` or `list.Repeat` by hand.
- With `--schemas out/` it also writes the converted schema of each module to `out/`.

A converted file that uses `#Foo` definitions or `let` is modern-only. With `auto`, the app then switches to the modern
engine, and v3 skips the file silently. The dry run shows which kind each file becomes.

### 6. Optional: try the modern engine

```sh
FLAMINGO_CONFIG_ENGINE=modern go run . config snapshot > snapshot-v4-modern-production.txt   # (planned)
diff snapshot-v4-production.txt snapshot-v4-modern-production.txt
```

The diff can show one line for `FLAMINGO_CONFIG_ENGINE`, because every environment variable becomes a key under
`flamingo.os.env`. Ignore that line.

A boot with the modern engine warns about every compat shim that changed the result, with file and line. To preview
v5, also set `flamingo.config.compat: false` *(planned)*. That gives pure CUE v0.17 semantics, and differences are
expected.

### 7. Go back to the legacy engine

Set `flamingo.config.engine: legacy` *(planned)* (or the environment variable or flag) and redeploy. If you converted
files with `config cue-migrate`, restore the originals first, because modern-only files need the modern engine.

To go back to v3 itself, revert the `go.mod` and import changes. Config files need no revert unless you converted
them. Sessions are not shared between v3 and v4 instances (see the [FAQ](#faq)).

## Choosing the engine

The engine is chosen once per application, not per area or file.

| Value | Meaning |
| --- | --- |
| `auto` | The default. The legacy engine if every file and schema of every area parses as legacy syntax. The modern engine as soon as one file or schema is modern-only, for example a module that ships `#Foo` |
| `legacy` | Always the legacy engine |
| `modern` | Always the modern engine |

The switch is read from the first of these that is set:

1. The flag `--flamingo-config-engine` *(planned)*.
2. The environment variable `FLAMINGO_CONFIG_ENGINE` *(planned)*.
3. The key `flamingo.config.engine` in the YAML of the root area *(planned)*.

Child areas cannot set the key.

When `auto` switches to the modern engine, it logs one line that names the file that caused the switch. Before it
chooses, Flamingo classifies every file and schema of the root area and all child areas. This includes modules that
`flamingo.modules.disabled` removes, because v3 also loads their schemas.

```yaml
flamingo:
  config:
    engine: legacy   # auto | legacy | modern
    compat: true     # false gives pure CUE v0.17 semantics
```

`flamingo.config.compat` *(planned)* is read the same way, with `--flamingo-config-compat` and
`FLAMINGO_CONFIG_COMPAT`. Its default is `true`, so the shims reproduce v3. The modern engine also keeps v3's file
merge. A later file replaces a scalar of an earlier file, and structs merge in place.

Flamingo sorts each file and schema into one of four classes.

| Class | Legacy engine | Modern engine |
| --- | --- | --- |
| Legacy-only | As v3 | Translated |
| Modern-only | Not possible, `auto` switches | As is |
| Both (most files) | As v3 | As is |
| Neither (old and new syntax in one file, or a typo) | Skipped with a warning | Skipped with a warning |

Files and modules of different syntax mix in the modern engine. A legacy reference `httpcache.Memory` becomes
`httpcache.#Memory` when the module declares `#Memory`. A modern reference to a module that still declares `Memory ::`
resolves the same way.

The modern engine differs from v3 in four cases that the shims cannot reproduce. `config compat` reports each one.

- A quotient in an interpolation prints other digits. `"\(8080/2)"` gives `4.04E+3` on v3 and `4040` on the modern
  engine. The decoded values are equal.
- List arithmetic with two reference operands inside a disjunction gives a different result.
- `>3 & <5 & int` gives 4 on v3 and a different result on the modern engine.
- A top-level `if` guard of the wrong type, such as `enabled: "yes"`, is kept by v3 and rejected by the modern engine.

Linking both engines makes the binary about 6 MB larger than on v3. The modern engine also loads slower. A prototype
took 14 to 20 ms per config load, and the legacy engine took 3 to 4 ms.

## Warnings and how to fix them

Warnings are printed once per boot, after the logger is set up. Repeated warnings are merged. Each warning has a code of
the form CUE001, CUE002 and so on, and names the file, the line and the fix. The codes and the wording are assigned in
the implementation, so the table lists warnings by kind.

A boot with the legacy engine prints the syntax warnings and one line that points to `config compat`. A boot with the
modern engine prints the syntax warnings and the shim warnings.

| Kind | Cause | Fix |
| --- | --- | --- |
| Definition | `Foo :: {…}` and references `Foo`, `a.Foo` | `#Foo: {…}`, `#Foo`, `a.#Foo` |
| Space-separated labels | `core zap: {…}` | `core: zap: {…}` |
| Template | `<Name>: {…}` | `[Name=string]: {…}` |
| Block comment | `/* … */` | `// …` on the line before |
| Trailing comprehension | `[x for x in y]`, `{"\(k)": v for k, v in s}`, `{y: 1 if c}` | `[for x in y {x}]`, `{for k, v in s {"\(k)": v}}`, `{if c {y: 1}}` |
| Alias | `X = e` | `let X = e` |
| Infix arithmetic | `a div b`, `a mod b`, `a quo b`, `a rem b` | `div(a, b)`, `mod(a, b)`, `quo(a, b)`, `rem(a, b)` |
| List arithmetic | `[1] + [2]`, `2 * [1]` | `list.Concat([1], [2])`, `list.Repeat([1], 2)`, by hand |
| Backquoted label | `` `a-b`: 1 `` and its references | `X="a-b": 1` with references `X`, or `y["a-b"]` |
| File skipped | A `.cue` file that cannot be parsed or mixes old and new syntax. v3 skipped it silently | Fix or remove the file. Its values count only after the fix, so take new snapshots |
| `CONTEXTFILE` entry not found | An entry that matches no `.yml`, `.yaml` or `.cue` file. v3 ignored it silently | Fix the path or remove the entry |
| `/v3` module path | A `flamingo.me/flamingo/v3/…` entry in `flamingo.modules.disabled`. v4 maps it to the module | Nothing is needed. Keep it while a rollback to v3 is possible, then use the `/v4` path |
| Shim changed a result (modern engine only) | A compat shim made the result differ from pure CUE v0.17. The warning names the key and the position | Nothing is needed. To adopt the new result, change the config so that no shim applies, or set `compat: false` |

`config cue-migrate` fixes all kinds from Definition to Backquoted label, except list arithmetic. A warning can also
point into a module's schema. Its maintainers fix those (see [For module authors](#for-module-authors)).

## Definitions renamed in modern syntax

Renaming is optional. The translator resolves a legacy and a modern reference to the same definition. Flamingo's own
schemas use syntax that both engines read during v4. In modern syntax, every segment of a nested name gets a `#`. For
example, `core.auth.fake.UserConfig` becomes `core.auth.#fake.#UserConfig`, and `commerce.CategoryTree` becomes
`commerce.#CategoryTree`. Check each module's release notes for its own definitions.

## For module authors

Your module keeps `CueConfig() string`, and a legacy schema keeps working.

1. Switch to modern syntax only when you need something that the legacy engine cannot read, such as `#Foo`, `let` or
   `[for …]` in a list. Until then, use syntax that both engines read: label paths `a: b: c:` (never `a b c:`),
   patterns `[Name=string]:`, `//` comments and leading comprehensions. A schema with `#` definitions moves every app
   that includes your module to the modern engine, which loads more slowly. An app that pins `engine: legacy` cannot
   use that schema. Ecosystem modules may switch once the translator can resolve references between legacy and modern
   syntax.
2. To convert, run `go run . config cue-migrate --schemas out/` *(planned)* in an app that includes your module. Paste
   the converted schema from `out/` into the `CueConfig()` string. For a schema built with `fmt.Sprintf`, the output is
   the converted rendered string, so edit the format string until the rendered result matches. Rename `Foo ::` to
   exactly `#Foo`.
3. Your users do not have to edit anything. A user file that still says `yourmodule.Foo` reaches your `#Foo` through
   the translator.
4. Test your schema on both engines. `config.TryModules` builds your schema with its `Depends()` modules, then fills
   and decodes it. An option *(planned)* runs the module set on both engines.

   ```go
   func TestModule_CueConfig(t *testing.T) {
   	t.Parallel()

   	if err := config.TryModules(nil, new(mymodule.Module)); err != nil {
   		t.Fatal(err)
   	}
   }
   ```

   Put every key that your schema requires into the `config.Map`, and add values that switch on its conditional parts.
   If the test fails with a Dingo injection error, or with an incomplete value from a framework key such as
   `flamingo.debug.mode`, add `new(framework.InitModule), new(zap.Module)` to the call. Leave out zap if your module
   binds `flamingo.Logger` itself. The signature of `config.TryModules` changes in v4.0.0-beta with dingo v2.
5. Run the test with `flamingo.config.compat: false` *(planned)* too. That is the planned default in v5. With it, a
   definition inside an `if` body rejects unknown keys, and an `if` on an optional field fails with
   `cannot reference optional field`. Give the field a default, or use a disjunction such as
   `*{enabled?: false} | {enabled: true, redirectTarget: string}`.
6. In the release notes, name the definitions that users may reference and any intended config change (keys added,
   removed or retyped), so that app owners can recognise it in their snapshot diff. If the schema is modern-only, say
   that the module needs the modern engine.

Do not add a package clause to a schema. Flamingo builds all schemas of an area in one package scope. A name that a
schema declares as both `#x` and `x` is refused as ambiguous.

## Errors and fixes

The legacy engine gives the errors of v3 with the same text. The modern engine formats every CUE error with
`errors.Details`, which lists each branch of a disjunction with its positions.

YAML values have no positions. For a type conflict or `field not allowed`, the value usually comes from your YAML, so
fix the key that the message names first.

| Error | Engine | Cause | Fix |
| --- | --- | --- | --- |
| `conflicting values (int \| *60000) and 5000 (mismatched types int and float)`, and on the modern engine `flamingo.router.timeout: conflicting values 5000 and int (mismatched types float and int)` | Both | YAML numbers arrive as floats and never fit an `int` key. v3 fails the same way | Set the key in CUE |
| `cannot convert incomplete value`, and on the modern engine `config key "…" is null …` *(planned message)* | Both | An unquoted `%%ENV:X%%` with X unset or empty, `~` or `null`, on a key with a default. v3 fails the same way | Set the variable, use `%%ENV:X%%default%%`, or remove the key |
| `field not allowed` | Both | A key that a closed definition does not declare. With `compat: false`, definitions inside an `if` are closed too | Fix or remove the key |
| A boot error that names a file, a construct and a fix *(planned message)* | Modern | The translator cannot convert a file or schema, and a modern-only file or schema exists. Otherwise `auto` falls back to the legacy engine | Fix the construct that the message names |
| A boot error that calls a name ambiguous *(planned message)* | Modern | A name is declared as both `#x` and `x` | Keep one spelling |
| `ERROR: legacy config mismatch for new "…"="…" and old "…"="…"` | Both | A legacy key and the key that replaced it have different values. v4 returns the same text as an error instead of stopping the process | Set only the new key |

## What changes in v5

- The modern engine becomes the default, and `flamingo.config.compat` defaults to `false`. Results then follow CUE
  v0.17 where it differs from v3.
- Legacy syntax still loads, with warnings. `flamingo.config.engine: legacy` selects the legacy engine.
- The legacy engine is removed in v6 at the earliest. Until then it stays available.

To prepare, run `config compat` in CI and settle every difference it reports. Then try `engine: modern` and
`compat: false` in a non-production deployment.

## FAQ

### Do number types change?

No. Numbers are `float64` as in v3, and integral values are also bound as `int` and `int64`.

### Is `'%%ENV:SECRET%%'` safe for a secret?

The quoted form becomes an empty string when the variable is unset, and the boot does not stop. This is the same on v3
and v4. Keep the quoted form, because the value stays a string. To make an empty value fail, add a constraint in a
`.cue` file: `mymodule: secret: !=""`. Do not use the unquoted form for secrets. The unquoted form is parsed as YAML, so
`yes` becomes `true` and `pass #word` silently becomes `pass`.

### Can v3 and v4 instances run side by side?

Yes for configuration, as long as they do not share files that you converted to modern syntax. Sessions are different.
Flamingo's own session types, such as the auth session data, are stored under a name that contains the module path. A
session with such a value that v3 wrote cannot be decoded by v4, and the other way round.

### Can `package flamingo` be used in `.cue` files?

No. In the first user file, v3 and the legacy engine stop the boot with
`package name "flamingo" conflicts with previous package name ""`. In later files, the `.cue` merge ignores package
clauses.

### Why does v4 include two engines?

CUE has no compatibility mode for v0.0.15, so the bundled copy is the only way to be identical to v3. It is frozen and
gets security and build fixes only.
