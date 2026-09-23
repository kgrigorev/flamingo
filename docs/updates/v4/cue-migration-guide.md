# Migrating configuration to Flamingo v4

> **Draft.** This guide follows the proposal in [cue-migration-strategy.md](cue-migration-strategy.md) and will be
> finalised with the v4.0.0 release. Commands marked *(planned)* do not exist yet. Their names and flags may still
> change before release.

Versions used in this guide:

- **v3.M**: the v3 release that ships the migration tooling, `config snapshot` and `config cue-migrate`
  *(planned)*.
- **v4.0.0**: the first v4 release. Release candidates are tagged `v4.0.0-rc.N`.

This guide covers configuration only. For other API changes, see the v4 release notes.

## Why

Flamingo validates your configuration, and fills in defaults, with CUE schemas that each module contributes. Flamingo
v3 uses CUE v0.0.15 from 2019. Flamingo v4 moves to the current CUE, v0.17.1, which removed several old syntax forms
and evaluates a few edge cases differently.

Flamingo v4 is built to keep the meaning of your configuration. For any configuration that loads on v3.M, v4 yields
the same keys, values and Go types, or fails at boot with an error that names the file and position. The steps below
confirm this for your application by comparing snapshots taken before and after.

## Am I affected?

Find out whether your app has `.cue` configuration:

```sh
find config -name '*.cue'                             # config directories of all areas
echo "$CONTEXTFILE" | tr ':' '\n' | grep '\.cue$'     # externally mounted files
```

- **You have `.cue` files.** Follow every step below.
- **YAML only.** You probably need no config edits, but you are still affected in four ways:
  1. **Dependencies.** Every Flamingo module you use, including third-party ones, needs a v4-compatible release. With
     an old module schema, v4 fails to boot whatever your config says.
  2. **Stricter validation.** Some mistakes that used to boot now fail loudly. An unknown key under a closed
     definition inside a conditional block is rejected, for example a typo under
     `commerce.checkout.placeorder.contextstore.redis`, `commerce.checkout.placeorder.lock.redis` or
     `commerce.product.fakeservice.sorting[*]`. So is a wrong-typed value on a switch that a schema tests in a
     condition: `flamingo.prefixrouter.rootRedirectHandler.enabled: "yes"` already fails on v3.M, others on v4.
  3. **Null values.** This covers `key: ~`, `key: null`, and an unquoted `%%ENV:X%%` whose variable is unset or empty,
     on a key whose schema has a default. v3 refuses to boot on these, and so does v4, only with a clearer message. A
     list key with a non-empty default becomes `[]` on both versions.
  4. **Error messages.** The wording of error messages changes.

  YAML-only apps do steps 1, 2, 4b, 5, 6 and 7.

## Before you start

- **Go.** Use the Go version that the v4.0.0 `go.mod` requires. CUE v0.17.1 itself needs Go 1.25 or later.
- **Deployed contexts.** List every one: each `CONTEXT` value, each `CONTEXTFILE` entry, each child area selected with
  `--flamingo-context`, and the environment variables each deployment sets.
- **Dependencies.** For every Flamingo-based dependency, find the v4-compatible release and read its notes. Look for
  renamed definitions and intended config changes.
- **Commands.** `go run .` below stands for however you start your application, for example `go run project.go`.

## Steps for application developers

### 1. Upgrade to v3.M and fix what strict loading reports

v3.M fails the boot when a `.cue` file cannot be parsed or a `CONTEXTFILE` entry names a missing file. v3 releases
without this check skipped such files silently, so your app may have been running without some of its
configuration. Boot every deployed context once and fix what is reported. If a file was never meant to load, remove
it.

`--flamingo-config-lenient` restores the old skipping for a transition period and logs each file it skips. v4 removes
the flag.

### 2. Take a baseline snapshot of every deployed context

Run this once per context, with that deployment's `CONTEXT`, `CONTEXTFILE` and environment variables. Either run it
in the deployment itself (for example as a one-off job) or export the same variables locally.

```sh
CONTEXT=production go run . config snapshot > snapshot-v3-production.tsv   # (planned)
```

- **Format.** Each line is one area and key: area, key, Go type and a hash of the value. `flamingo.os.env` is left
  out. An area that fails to load appears as a single error line, which is worth fixing now.
- **Keep snapshots private.** Values are hashed, but low-entropy values such as booleans and ports can be guessed from
  an unsalted hash. To share a snapshot, pass the same `--salt <secret>` for the v3 and the v4 snapshot.
- **Don't use `config` dumps instead.** They contain every environment variable and secret in clear text.

### 3. Pre-migrate dual-valid syntax on v3 (only if you have `.cue` files)

This step is recommended and optional. It shrinks the cutover to what is truly version-specific, and you can ship it
on v3 like any other change.

```sh
find config -name 'config*.cue' -exec go run cuelang.org/go/cmd/cue@v0.0.15 fmt {} +
go run cuelang.org/go/cmd/cue@v0.0.15 fmt /path/to/contextfile.cue      # each CONTEXTFILE .cue entry
```

The official v0.0.15 formatter makes these rewrites, and CUE v0.0.15 and v0.17.1 read the result identically:

| Before | After |
| --- | --- |
| `core zap: {…}` | `core: zap: {…}` |
| `<Name>: {…}` | `[Name=_]: {…}` (`[string]: {…}` if `Name` is unused) |
| `/* … */` | `// …`, moved to the line before |
| `{"\(k)": v for k, v in s}` | `{for k, v in s {"\(k)": v}}` |

Take the snapshots from step 2 again. The diff must be empty. Commit the change and deploy it on v3.

Do not hand-write v4-only syntax on v3: `#Foo`, `let`, `[for …]`, `a!:`, `div()`, `list.Concat`. v3.M rejects such a
file at boot, and v3 releases without strict loading silently ignore it.

### 4. Create the migration branch (one commit)

**a. Convert the version-locked constructs while still on v3.M, before touching `go.mod`.**

```sh
go run . config cue-migrate            # (planned) dry run: prints the planned changes, writes nothing
go run . config cue-migrate --write    # (planned)
```

The command:

- **reads** every `config*.cue` file in every area directory, the `.cue` entries of `CONTEXTFILE`, and any file you
  name;
- **resolves** references against your app's own module schemas, so run it from your normal main package;
- **renames** definitions and every reference to them: `Foo :: {…}` becomes `#Foo: {…}`, `core.auth.http` becomes
  `core.auth.#http`, and both segments of a nested name change (`core.auth.fake.UserConfig` becomes
  `core.auth.#fake.#UserConfig`);
- **rewrites** `[x for x in y]` to `[for x in y {x}]`, `X = e` to `let X = e`, and `a div b` to `div(a, b)` (and
  likewise `mod`, `quo`, `rem`), and also makes the step 3 rewrites;
- **stops** when it cannot classify a reference: it names the reference and writes nothing. This usually means the
  module that declares it is not part of your app. Add it, or edit that reference by hand using the
  [rename table](#definitions-renamed-in-v4).

After `--write`, v3.M no longer boots with these files. That is expected: continue with 4b in the same branch.

Some constructs need extra care:

- **List arithmetic** (`[1] + [2]`, `2 * [1]`) is not converted. v4 reports
  `Addition of lists is superseded by list.Concat`. Run `go run cuelang.org/go/cmd/cue@v0.17.1 fix ./path/file.cue`,
  which handles literal operands, or write `list.Concat` / `list.Repeat` yourself.
- **Do not use `cue fix` for `::`.** Current releases exit 0 without changing anything. The v0.2.x releases add
  bridge fields that become new config keys, and they hang on recursive definitions.
- **Never replace `::` with `:`.** That turns a schema-only definition into a regular config key.

**b. Move to v4 and upgrade your dependencies.**

```sh
find . -name '*.go' -exec perl -pi -e 's#flamingo\.me/flamingo/v3#flamingo.me/flamingo/v4#g' {} +
go get flamingo.me/flamingo/v4@v4.0.0
go get <each Flamingo-based dependency>@<its v4-compatible release>
go mod tidy && go build ./... && go test ./...
```

### 5. Take v4 snapshots and compare

```sh
CONTEXT=production go run . config snapshot > snapshot-v4-production.tsv   # (planned)
diff snapshot-v3-production.tsv snapshot-v4-production.tsv
```

The diff must be empty for every context. The only accepted lines are changes that a dependency announces in its
release notes. What a non-empty diff usually means:

| Diff shows | Usual cause |
| --- | --- |
| Keys missing, or a different value | A `.cue` file or reference was not converted, or a file for this context was not part of the run in step 4a |
| A new top-level key named like a definition | `Foo ::` was changed to `Foo:` instead of `#Foo:` |
| Same value, different Go type | A bug. Report it with the two lines |
| An area error line | Read the boot error in that context (see [Errors and fixes](#errors-and-fixes)) |

### 6. Deploy config and binary together

Ship the converted `.cue` files and the v4 binary in the same artifact. v3 and v4 instances must never read the same
converted `.cue` files. During a rolling or canary deploy, give externally mounted `CONTEXTFILE` files a separate
path per version, or move them to YAML, which means the same on both versions. YAML-only apps can roll out gradually.

### 7. Roll back if needed

Revert the migration commit, then redeploy the binary and config together. Roll back to v3.M rather than an older v3
release: releases without strict loading silently ignore a converted `.cue` file that was left in place.

## Definitions renamed in v4

These definition paths may appear in your `.cue` files:

| v3 reference | v4 reference |
| --- | --- |
| `core.auth.http` | `core.auth.#http` |
| `core.auth.fake` | `core.auth.#fake` |
| `core.auth.fake.UserConfig` | `core.auth.#fake.#UserConfig` |
| `core.auth.oauth2Config` | `core.auth.#oauth2Config` |
| `core.auth.oidc` | `core.auth.#oidc` |
| `customOidcBroker`, `StaticAuthBroker` (example modules in `core/auth/example/custom`) | `#customOidcBroker`, `#StaticAuthBroker` |
| `commerce.CategoryTree`, `commerce.CategoryTreeNode` | `commerce.#CategoryTree`, `commerce.#CategoryTreeNode` |
| `commerce.SearchSorting` | `commerce.#SearchSorting` |
| `commerce.checkout.Redis` | `commerce.checkout.#Redis` |

Definitions from third-party modules follow the same rule, `Foo` → `#Foo`. Check each module's release notes.

## For module authors

1. **Test your schema.** Add a test that builds your schema together with its `Depends()` modules, then fills and
   decodes it:

   ```go
   func TestModule_CueConfig(t *testing.T) {
   	t.Parallel()

   	if err := config.TryModules(nil, new(mymodule.Module)); err != nil {
   		t.Fatal(err)
   	}
   }
   ```

   To exercise the conditional parts of the schema, pass a `config.Map` with values that switch them on. Run the test
   on v3.M, and on your v4 branch against a v4.0.0 release candidate.
2. **Stay in the dual-valid subset.** These mean the same on v3 and v4: label paths `a: b: c:` (never `a b c:`),
   patterns `[Name=string]:`, `//` comments, leading comprehensions (`for k, v in s {…}`, `if c {…}`), and
   references to other modules' keys (v4 puts all schemas into one package). Avoid two things. Don't put an `if` on
   an optional field: give it a default or use a disjunction such as
   `*{enabled?: false} | {enabled: true, redirectTarget: string}`. And don't add a package clause: v3 rejects it, and
   v4 adds one for you.
3. **Know what needs different v3 and v4 text.** That is definitions (`Foo ::` on v3, `#Foo:` on v4) and references
   to them, `[e for x in y]`, aliases `X = e` (a hidden field `_X` works on both), infix `div`/`mod`/`quo`/`rem`, and
   list arithmetic. Your v4 release needs the `/v4` imports anyway, so every module needs a new release. A dual-valid
   schema lets you fix and test it on v3 first.
4. **Convert Go-embedded schemas.** In an app on v3.M that includes your module, run
   `go run . config cue-migrate --schemas out/` *(planned)*. It writes every module's converted schema to `out/`.
   Paste yours back into the `CueConfig()` string on your v4 branch.
   - For a schema built with `fmt.Sprintf`, the output is the converted *rendered* string. Edit the format string by
     hand until the rendered result matches.
   - Rename `Foo ::` to exactly `#Foo`. Your users' files are converted with that rule.
5. **Closedness inside `if` is now enforced.** A definition used inside an `if` block rejects unknown keys on v4 but
   did not on v3. Mention this in your release notes.
6. **Release notes.** List the definitions users may reference, with their new names. Also list any intended config
   change (keys added, removed or retyped), so that app owners can recognise it in their snapshot diff.

## Errors and fixes

CUE messages are shown as CUE v0.17.1 prints them, and v4 prefixes them with the file and position. The last three
rows are Flamingo's own messages. If the file name has the form `<import path>.<Type>:line:col`, the error is in a
module's schema, not in your config, so upgrade that module.

| Error | Cause | Fix |
| --- | --- | --- |
| `expected operand, found ':'` | `Foo :: {…}` definition or `<Name>: {…}` template | `config cue-migrate` (step 4a) or `cue fmt` v0.0.15 (step 3) |
| `expected label or ':', found 'IDENT' zap` | Space-separated labels `core zap:`, or an alias `X = 3` | `cue fmt` v0.0.15 (labels) or `config cue-migrate` (aliases) |
| `expected operand, found '/'` | A `/* … */` comment | `cue fmt` v0.0.15 |
| `expected ']', found 'for'` | Trailing list comprehension `[x for x in y]` | `config cue-migrate` |
| `missing ',' in struct literal` | Trailing struct comprehension, or infix `div`/`mod`/`quo`/`rem` | `cue fmt` v0.0.15 or `config cue-migrate` |
| `Addition of lists is superseded by list.Concat` | List arithmetic | `cue fix` v0.17.1, or `list.Concat` / `list.Repeat` |
| `undefined field: http` (e.g. `H: undefined field: http`) | A reference that still uses the v3 name of a definition | [Rename](#definitions-renamed-in-v4) it; for nested names rename both segments |
| `reference "customOidcBroker" not found` | A bare reference to a definition that is now `#customOidcBroker` | Add `#` |
| `cannot reference optional field: enabled` | A module schema with an `if` on an optional field | Upgrade the module; module authors, see above |
| `field not allowed` (e.g. `commerce.checkout.placeorder.contextstore.redis.typo: field not allowed`) | A key that the closed definition does not declare. v3 did not check this inside conditional blocks | Fix or remove the key |
| `2 errors in empty disjunction:`, followed by lines like `conflicting values 5E+3 and int (mismatched types float and int)` | The value matches no alternative of the key's schema. Example: a YAML integer for an `int` key, which v3 rejects too | Use a value of the listed type, or set the key in CUE |
| `expected operand, found 'ILLEGAL'` (on v3.M) | A v4-only construct such as `#Foo` in a file loaded by v3 | Deploy the file with the v4 binary, or revert it |
| `config key "…" is null … but its schema default is …` *(planned message)* | An unquoted `%%ENV:X%%` with X unset or empty, `~` or `null`, on a key with a default. v3 refused this too (`cannot convert incomplete value`) | Set the variable, use `%%ENV:X%%default%%`, or remove the key |
| `CONTEXTFILE entry matches no file: "…"` *(planned message)* | A `CONTEXTFILE` path with no `.yml`, `.yaml` or `.cue` file. v3 releases without strict loading ignored it | Fix the path |
| `legacy config mismatch for new "…"="…" and old "…"="…"` | A legacy key and its new key are set to different values. This is unchanged from v3 | Set only the new key |

## FAQ

**Do number types change?**
No. Numbers are `float64` as in v3, and integral values are still also bound as `int` and `int64`.

**My secret is `'%%ENV:SECRET%%'`. Is that safe?**
The quoted form becomes an empty string when the variable is unset, on v3 and v4 alike. The unquoted form becomes
null, which fails at boot unless the key's schema allows null. Use the unquoted form for required values and
`%%ENV:X%%default%%` for optional ones.

**Can I keep `.cue` files that work on both v3 and v4?**
Yes, as long as they use only dual-valid syntax (step 3) and do not reference definitions.

**Can v3 and v4 instances run side by side?**
Yes, as long as they do not share converted `.cue` files. YAML is read the same by both.

**Can I use `package flamingo` in my `.cue` files?**
v4 accepts it, but v3 rejects it. Any other package name fails on both.

**Why not upgrade CUE in smaller steps?**
No intermediate CUE version avoids the breaking changes, and CUE has no compatibility mode. The snapshot comparison
is the safety net instead.

**Should I set `CUE_EXPERIMENT` or `CUE_DEBUG`?**
No. v4 inherits them from the environment through CUE, so leave them unset.
