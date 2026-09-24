# Migrating configuration to Flamingo v4

> **Draft.** This guide follows the proposal in [cue-migration-strategy.md](cue-migration-strategy.md) and will be
> finalised with the v4.0.0 release. Commands marked *(planned)* do not exist yet. Their names and flags may still
> change before release.

Versions used in this guide:

- **v3.N**: the v3 release with strict `.cue` loading and `config snapshot` *(planned)*.
- **v3.M**: a later v3 release that adds `config cue-migrate` and contains everything in v3.N *(planned)*.
- **v4.0.0**: the first v4 release. Release candidates are tagged `v4.0.0-rc.1`, `v4.0.0-rc.2` and so on.

This guide covers configuration only. For other API changes, see the v4 release notes.

## Why

Flamingo validates your configuration, and fills in defaults, with CUE schemas that each module contributes. Flamingo
v3 uses CUE v0.0.15 from 2019. Flamingo v4 moves to the current CUE, v0.17.1, which removed several old syntax forms
and evaluates a few edge cases differently.

Flamingo v4 is built to keep the meaning of your configuration. For any configuration that loads on v3.N, v4 yields
the same keys, values and Go types, or fails at boot with an error that names the file position or the config key.
The steps below confirm this for your application by comparing snapshots taken before and after.

## Am I affected?

Find out whether your app has `.cue` configuration or schemas of its own:

```sh
find config -name '*.cue'        # config directories of all areas (or the directory passed to flamingo.ConfigDir)
# externally mounted files: the loader strips each CONTEXTFILE entry's extension and also loads <entry>.cue
echo "$CONTEXTFILE" | tr ':' '\n' | sed 's/\.[^./]*$//' | while read -r f; do
  [ -n "$f" ] && [ -f "$f.cue" ] && echo "$f.cue"
done                             # run this with each deployment's CONTEXTFILE
grep -rl --include='*.go' --exclude-dir=vendor 'CueConfig()' .   # your own modules with embedded schemas
```

- **You have `.cue` files.** Follow every step below.
- **Your own modules have `CueConfig()`.** Convert them like a module author ([For module authors](#for-module-authors),
  items 2 to 4), even if you have no `.cue` files.
- **YAML only.** You probably need no config edits, but you are still affected in four ways:
  1. **Dependencies.** Every Flamingo module you use, including third-party ones, needs a v4-compatible release. With
     an old module schema, v4 fails to boot whatever your config says. A dependency still built for v3 either fails
     `go build` or, if it uses only packages without config, builds and silently loses its bindings. The check in
     step 4b finds it.
  2. **Stricter validation.** Some mistakes that used to boot now fail loudly. A typo in a key that a module only
     checks when a switch is on is rejected, for example under `commerce.checkout.placeorder.contextstore.redis`,
     `commerce.checkout.placeorder.lock.redis` or `commerce.product.fakeservice.sorting[*]`. So is a wrong-typed
     `flamingo.prefixrouter.rootRedirectHandler.enabled: "yes"`, from v3.N on (other known schemas already reject
     wrong-typed switches on v3).
  3. **Null values.** This covers `key: ~`, `key: null`, and an unquoted `%%ENV:X%%` whose variable is unset or empty,
     on a key whose schema has a default. v3 refuses to boot on these, and so does v4, only with a clearer message. A
     list key with a non-empty default becomes `[]` on both versions.
  4. **Error messages.** The wording of error messages changes.

  YAML-only apps do steps 1, 2, 4b, 5, 6, 7 and 8, plus the v3.M upgrade and 4a (step 4) if their own modules have
  `CueConfig()`.

## Before you start

- **Go.** Use the Go version that the v4.0.0 `go.mod` requires. CUE v0.17.1 itself needs Go 1.25 or later.
- **Deployed contexts.** List every one: each `CONTEXT` value, each `CONTEXTFILE` entry, each child area selected with
  `--flamingo-context`, and the environment variables each deployment sets.
- **Dependencies.** For every Flamingo-based dependency, find the v4-compatible release and read its notes. Look for
  renamed definitions and intended config changes.
- **Commands.** `go run .` below stands for however you start your application, for example `go run project.go`.

## Steps for application developers

### 1. Upgrade to v3.N or later and fix what strict loading reports

v3.N fails the boot when a `.cue` file cannot be parsed or a `CONTEXTFILE` entry names a missing file. v3 releases
without this check skipped such files silently, so your app may have been running without some of its
configuration. If a file was never meant to load, remove it.

`--flamingo-config-lenient` restores the old skipping for a transition period and logs each file it skips. Roll v3.N
out with this flag first in every environment, fix each file the log reports as skipped, then remove the flag. v4
removes it.

### 2. Take a baseline snapshot of every deployed context

Run this once per context (v3.N or later), with that deployment's `CONTEXT`, `CONTEXTFILE`, environment variables
and flags (`--flamingo-context`, `--flamingo-config`). Either run it in the deployment itself (for example as a
one-off job) or export the same variables locally, with placeholder values for secrets that you reuse for the v4
snapshot.

```sh
CONTEXT=production go run . config snapshot > snapshot-v3-production.tsv   # (planned)
```

- **Format.** The first line is the header `# flamingo config snapshot 1`. Releases before v3.N have no such subcommand
  and print `snapshot:` and `null` instead. Each further line is one area and key: area, key, Go type and a hash of the
  value, plus one line per module that `flamingo.modules.disabled` removes. `flamingo.os.env`, `flamingo.cmd.name` and
  `cmd.name` are left out. A child area that fails to load appears as a single error line, which is worth fixing now. If
  the root or the selected area fails, the command stops with the boot error.
- **Keep snapshots private.** Values are hashed, but low-entropy values such as booleans and ports can be guessed from
  an unsalted hash. To share a snapshot, pass the same `--salt <secret>` for the v3 and the v4 snapshot.
- **Don't use `config` dumps instead.** They contain every environment variable and secret in clear text.

### 3. Pre-migrate dual-valid syntax on v3 (only if you have `.cue` files)

This step is recommended and optional. It shrinks the cutover to what is truly version-specific, and you can ship it
on v3 like any other change.

```sh
find config -name 'config*.cue' -exec go run cuelang.org/go/cmd/cue@v0.0.15 fmt {} +
go run cuelang.org/go/cmd/cue@v0.0.15 fmt migrate/site.cue   # a working copy of each CONTEXTFILE .cue file
```

Work on copies of externally mounted files, not on the live mount, and publish the result like any other config
change.

The official v0.0.15 formatter makes these rewrites, and CUE v0.0.15 and v0.17.1 read the result identically:

| Before | After |
| --- | --- |
| `core zap: {…}` | `core: zap: {…}` |
| `<Name>: {…}` | `[Name=_]: {…}` (`[string]: {…}` if `Name` is unused) |
| `/* … */` | `// …`, moved to the line before |
| `{"\(k)": v for k, v in s}` | `{for k, v in s {"\(k)": v}}` |

Backquoted labels that are not identifiers, such as `` `a-b` ``, stay as they are, and a referenced one gets an alias
such as `` X1=`a-b` `` that v4 still cannot parse. Step 4a converts them. Unquoting a backquoted identifier such as
`` `c` `` can change how v3 merges your files (a conflict can become an override); the new snapshot below shows it.

Take the snapshots from step 2 again. The diff must be empty. Commit the change and deploy it on v3.

Do not hand-write v4-only syntax on v3: `#Foo`, `let`, `[for …]`, `a!:`. v3.N and later reject such a file at boot,
and older v3 releases silently ignore it. `div()` and `list.Concat` fail at boot on every v3 release.

### 4. Create the migration branch (one commit)

**Upgrade to v3.M first.** Run `go get flamingo.me/flamingo/v3@<v3.M>`, commit, and deploy it like any v3 minor
release. Take the step 2 baselines again from this commit: it is the parent of the migration commit.

Untracked `.cue` files, typically `config_local.cue`, are not in the migration commit. Ask everyone to convert theirs
on v3.M before switching to the v4 branch (`go run . config cue-migrate --write` *(planned)*, then discard the changes
to tracked files), or to move them to YAML.

**a. Convert the rest (definitions, list comprehensions, aliases, infix `div`, and the backquoted labels that step 3
left), while still on v3.M and before moving `go.mod` to v4.**

```sh
go run . config cue-migrate                           # (planned) dry run: prints the planned changes, writes nothing
go run . config cue-migrate --write --schemas out/    # (planned) --schemas only if your own modules have CueConfig()
```

The command:

- **boots** your app on v3.M, so run it from your normal main package with the `CONTEXT` and environment variables of
  a successful local boot, for example your dev settings;
- **reads** every `config*.cue` file in every area directory, for each `CONTEXTFILE` entry the `.cue` file the loader
  would load, and any file you name;
- **resolves** references against your app's own module schemas;
- **renames** definitions and every reference to them: `Foo :: {…}` becomes `#Foo: {…}`, `core.auth.http` becomes
  `core.auth.#http`, and both segments of a nested name change (`core.auth.fake.UserConfig` becomes
  `core.auth.#fake.#UserConfig`);
- **rewrites** `[x for x in y]` to `[for x in y {x}]`, `X = e` to `let X = e`, `a div b` to `div(a, b)` (and
  likewise `mod`, `quo`, `rem`), and `` `a-b`: 1 `` to `X="a-b": 1` with references `X`, and also makes the step 3
  rewrites;
- **stops** when it cannot classify a reference: it names the reference and writes nothing. This usually means the
  module that declares it is not part of your app. Add it, or edit that reference by hand using the
  [rename table](#definitions-renamed-in-v4);
- **also stops** on a reference that the `.cue` merge left pointing at a dropped declaration, on an alias in a later
  file whose expression references a field (replace it with the value v3 used, or move it and the fields that use it
  into `config.cue`), and on a backquoted label that has no spelling for v4 in its file (see
  [Errors and fixes](#errors-and-fixes)). Fix these on v3 and take a new baseline (step 2) before you convert;
- **writes**, with `--schemas out/`, every module's converted schema to `out/`, with or without `--write`. Paste your
  own modules' output back into their `CueConfig()` strings in 4b.

**Externally mounted files.** Copy each one into your working tree, and for the run point `CONTEXTFILE` at the copies
(or leave it unset and name the copies, as in `go run . config cue-migrate --write migrate/site.cue` *(planned)*),
so that `--write` does not rewrite the live mount. Publish the result at a new path for v4 (step 6), and keep the v3
copy for a rollback (step 7).

After `--write`, v3.M no longer boots with these files, so the command cannot run again. That is expected: continue
with 4b in the same branch. To redo the conversion, for example to add a file that step 5 shows you missed, restore
the originals first (`git checkout -- config/` plus your saved copies of external files), then repeat 4a with every
file included.

Some constructs need extra care:

- **List arithmetic** (`[1] + [2]`, `2 * [1]`) is not converted. v4 reports
  `Addition of lists is superseded by list.Concat` or `Multiplication of lists is superseded by list.Repeat`. After
  `--write`, run `go run cuelang.org/go/cmd/cue@v0.17.1 fix ./path/file.cue`, which handles literal operands, or
  write `list.Concat` / `list.Repeat` yourself. Do not use any `cue fix` for `::`: current releases exit 0 without
  changing anything, and v0.2.x adds bridge fields that become new config keys and hangs on recursive definitions.
- **Never replace `::` with `:`.** That turns a schema-only definition into a regular config key.

**b. Move to v4 and upgrade your dependencies.**

```sh
find . -name '*.go' -not -path './vendor/*' \
  -exec perl -pi -e 's#flamingo\.me/flamingo/v3#flamingo.me/flamingo/v4#g' {} +
go get flamingo.me/flamingo/v4@v4.0.0
go get <each Flamingo-based dependency>@<its v4-compatible release>
go mod tidy && go build ./... && go test ./...   # with vendoring: go mod tidy && go mod vendor && go build ./...
go list -deps ./... | grep '^flamingo.me/flamingo/v3/'   # must print nothing
grep -rn 'flamingo.me/flamingo/v3' --exclude-dir=vendor --exclude-dir=.git --exclude=go.sum . | grep -v '\.go:'
```

- **References outside Go files.** The last command lists build scripts, `replace` lines and tool configs that still
  name v3. Update them, in particular `-ldflags "-X flamingo.me/flamingo/v3/framework/flamingo.appVersion=…"`: the
  linker silently ignores an `-X` for a symbol that no longer exists, and `version` then prints `develop`. Keep the
  `flamingo.modules.disabled` entries (below).

- **Dependencies still built for v3.** Output from the `go list` line is a dependency that still imports Flamingo v3.
  One that uses Flamingo v3's web or config packages fails `go build`. One that uses only `framework/flamingo` (event
  subscribers, template functions) builds, and v4 silently ignores those bindings. Upgrade it. If a dependency's
  v4-compatible release has a new module path, rewrite its imports the same way.
- **Module paths in config.** Leave entries for Flamingo's own modules in `flamingo.modules.disabled` on their v3
  paths: v4 maps `flamingo.me/flamingo/v3/…` to `/v4/…` and logs a deprecation warning. For a dependency whose
  v4-compatible release has a new module path, add the new path next to the old one; v3.N and later ignore the entry
  they cannot match. Remove the old paths once you no longer need to roll back.
- **Your own `CueConfig()` strings.** Replace them with the converted schemas from `out/`.

### 5. Take v4 snapshots and compare

```sh
CONTEXT=production go run . config snapshot > snapshot-v4-production.tsv   # (planned)
diff snapshot-v3-production.tsv snapshot-v4-production.tsv
```

- **Take each v4 snapshot the same way as its v3 baseline:** the same `CONTEXT`, environment variables and flags, and
  `CONTEXTFILE` pointing at the converted copies. If the baseline came from a one-off job in the deployment, run the
  v4 build there as a one-off job before rollout. A boot error or an area error line there is a failure you would
  otherwise meet during the rollout.
- **If you cannot run in the deployment,** give both runs the same placeholder values instead of copying secrets to
  your machine. That still compares keys and types, and leaves failures that depend on production values to the
  canary.
- **If the baseline is older than your migration branch,** take it again from the branch's parent commit, so that
  unrelated config changes do not show up in the diff.

The diff must be empty for every context. The only accepted lines are changes that a dependency announces in its
release notes, and quotients in interpolations (below). What a non-empty diff usually means:

| Diff shows | Usual cause |
| --- | --- |
| Keys missing, or a different value | A `.cue` file or reference was not converted, or a file for this context was not part of the run in step 4a |
| A new top-level key named like a definition | `Foo ::` was changed to `Foo:` instead of `#Foo:` |
| Same value, different Go type | A bug. Report it with the two lines |
| A different set of disabled modules | A `flamingo.modules.disabled` entry did not match (read the boot log's warnings), or a dependency's module path changed (expected if you listed its new path in step 4b) |
| A different value for a key whose `.cue` expression divides (`/`) inside an interpolation | CUE v0.17.1 prints quotients differently. On v3, write the result as a literal and take a new baseline (step 2), or accept the new text |
| An area error line, or the command stops with a boot error | Read the boot error in that context (see [Errors and fixes](#errors-and-fixes)) |

### 6. Deploy config and binary together

Ship the converted `.cue` files and the v4 binary in the same artifact. v3 and v4 instances must never read the same
converted `.cue` files. During a rolling or canary deploy, give externally mounted `CONTEXTFILE` files a separate
path per version. Moving a `.cue` file to YAML instead is a config change even on v3: do it on v3 first and take a new
baseline (step 2). Configuration-wise, YAML-only apps can roll out gradually; for sessions, see the [FAQ](#faq).

### 7. Roll back if needed

Revert the migration commit, point `CONTEXTFILE` back at the v3 copies of externally mounted files (reverting the
commit does not do that), and redeploy the binary and config together. Roll back to v3.N or later, without
`--flamingo-config-lenient`, rather than an older v3 release: releases without strict loading silently ignore a
converted `.cue` file that was left in place.

### 8. Keep the check

Commit the v4 snapshot of each context that CI can reproduce (with placeholder values, and `--salt` from a CI secret
unless the repository is private), and compare it in CI with `config snapshot`. A dependency update that selects a
newer `cuelang.org/go` than v4 was validated with then fails your tests instead of silently changing configuration.

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

   Put every key that your schema requires into the `config.Map`, and to exercise the conditional parts of the
   schema, add values that switch them on. `TryModules` also initialises Dingo for your module and its `Depends()`.
   If it fails with a Dingo injection error, or with an incomplete value that comes from a framework key such as
   `flamingo.debug.mode`, add `new(framework.InitModule), new(zap.Module)` to the call (leave out zap if your module
   binds `flamingo.Logger` itself). Run the test on v3.N or later, and on your v4 branch against a v4.0.0 release
   candidate.
2. **Stay in the dual-valid subset.** These mean the same on v3 and v4: label paths `a: b: c:` (never `a b c:`),
   patterns `[Name=string]:`, `//` comments, leading comprehensions (`for k, v in s {…}`, `if c {…}`), and
   references to other modules' keys (v4 puts all schemas into one package). Avoid two things. Don't put an `if` on
   an optional field: give it a default or use a disjunction such as
   `*{enabled?: false} | {enabled: true, redirectTarget: string}`. And don't add a package clause: v3 rejects it, and
   v4 adds one for you.
3. **Know what needs different v3 and v4 text.** That is definitions (`Foo ::` on v3, `#Foo:` on v4) and references
   to them, `[e for x in y]`, aliases `X = e`, infix `div`/`mod`/`quo`/`rem`, and list arithmetic. A hidden field
   works on both versions instead of an alias, but hidden fields are shared by all schemas and user files, so give it
   a name unique to your module, such as `_mymoduleX`. Your v4 release needs the `/v4` imports anyway, so every
   module needs a new release. A dual-valid schema lets you fix and test it on v3 first.
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

CUE messages are shown as CUE v0.17.1 prints them. v4 lists every branch of a disjunction error, each with its
positions. The last four rows are Flamingo's own messages.

Positions of the form `<import path>.<Type>:line:col` point into a module's schema. If the message is a parse error
or `reference … not found`, the schema itself is broken: upgrade that module, or convert it if it is one of your own.
If it is a type conflict or `field not allowed`, the value usually comes from your YAML, which has no positions: fix
the key named at the start of the message.

| Error | Cause | Fix |
| --- | --- | --- |
| `expected operand, found ':'` | `Foo :: {…}` definition or `<Name>: {…}` template | `config cue-migrate` (step 4a) or `cue fmt` v0.0.15 (step 3) |
| `expected label or ':', found 'IDENT' zap` | Space-separated labels `core zap:` | `cue fmt` v0.0.15 |
| `expected label or ':', found …` with the next token, such as `'IDENT' y`, `newline`, `','`, `'}'` or `'EOF'` (the position can be on the line after the alias) | An alias `X = 3` | `config cue-migrate` |
| `expected operand, found '/'` | A `/* … */` comment | `cue fmt` v0.0.15 |
| `expected ']', found 'for'` | Trailing list comprehension `[x for x in y]` | `config cue-migrate` |
| `missing ',' in struct literal` | Trailing struct comprehension, or infix `div`/`mod`/`quo`/`rem` | `cue fmt` v0.0.15 or `config cue-migrate` |
| `` illegal character U+0060 '`' `` | A backquoted label or reference such as `` `a-b` `` | `config cue-migrate` (step 4a). Where it stops, move that key or reference into `config.cue`, or the key into YAML |
| `Addition of lists is superseded by list.Concat`, `Multiplication of lists is superseded by list.Repeat` | List arithmetic | `cue fix` v0.17.1 after step 4a, or `list.Concat` / `list.Repeat` |
| `undefined field: http` (e.g. `H: undefined field: http`) | A reference that still uses the v3 name of a definition | [Rename](#definitions-renamed-in-v4) it; for nested names rename both segments |
| `reference "customOidcBroker" not found` | A bare reference to a definition that is now `#customOidcBroker` | Add `#` |
| `alias "X" redeclared in same scope` (the position can be in the earlier file) | Two user files of one area declare the same `let` name (converted from aliases) | Rename one of them |
| `cannot have both alias and field with name "X" in same scope` | A later file's `let X` (converted from an alias) next to a field `X` from an earlier file | Rename the `let` and its references |
| `cannot reference optional field: enabled` | A module schema with an `if` on an optional field | Upgrade the module; module authors, see above |
| `field not allowed` (e.g. `commerce.checkout.placeorder.contextstore.redis.typo: field not allowed`) | A key that the schema does not declare. v3 did not check this in parts of a schema that a switch turns on | Fix or remove the key |
| `2 errors in empty disjunction:`, followed by lines like `conflicting values 5000 and int (mismatched types float and int)` | The value matches no alternative of the key's schema. Example: a YAML integer for an `int` key, which v3 rejects too | Use a value of the listed type, or set the key in CUE |
| On v3.N or later: `expected operand, found 'ILLEGAL'` (`#Foo`), `expected selector, found 'ILLEGAL'` (`core.auth.#http`), `expected '{', found 'let'`, `expected operand, found 'for'`, `` expected label or ':', found '!' ``, `expected operand, found 'ATTRIBUTE'` | A v4-only construct in a file loaded by v3 | Deploy the file with the v4 binary, or revert it (for a `CONTEXTFILE` file, point it back at the v3 copy) |
| `this reference points at a declaration that the .cue merge dropped …` *(planned message)*, in any `.cue` file of an area that loads more than one | The merge replaced what the reference names: a later file references a struct label that an earlier file also declares, a file declares the referenced top-level label more than once (two `flamingo:` lines in `config.cue`), or a reference names a scalar that a later file overrides. v3 failed on it or silently used the default, `{}` or nothing | To keep v3's result, replace the expression with the value v3 used. To use the reference, on v3 declare its top-level label only once in `config.cue` (one `flamingo: {…}` block) and reference it from there; for a scalar that a later file overrides, make the `config.cue` value a default (`port: *3322 \| number`) and set the override in YAML. Then take a new baseline (step 2) |
| `config key "…" is null … but its schema default is …` *(planned message)* | An unquoted `%%ENV:X%%` with X unset or empty, `~` or `null`, on a key with a default. v3 refused this too (`cannot convert incomplete value`) | Set the variable, use `%%ENV:X%%default%%`, or remove the key |
| `CONTEXTFILE entry matches no file: "…"` *(planned message)* | A `CONTEXTFILE` path with no `.yml`, `.yaml` or `.cue` file. v3 releases without strict loading ignored it | Fix the path |
| `legacy config mismatch for new "…"="…" and old "…"="…"` | A legacy key and its new key are set to different values. This is unchanged from v3 | Set only the new key |

## FAQ

**Do number types change?**
No. Numbers are `float64` as in v3, and integral values are still also bound as `int` and `int64`. Their text in a
CUE interpolation stays the same too, except for quotients: `"\(8080/2)"` prints `4.04E+3` on v3 and `4040` on v4,
`"\(7/7)"` prints `1` and `1.0`. The snapshot comparison shows such a change.

**My secret is `'%%ENV:SECRET%%'`. Is that safe?**
The quoted form becomes an empty string when the variable is unset, on v3 and v4 alike, and the boot does not stop.
Keep the quoted form: it keeps the value a string, although a `'` in the value stops the boot with a YAML error and
a line break becomes a space. Make an empty value fail with a constraint in a `.cue` file that works on both versions:
`mymodule: secret: !=""`. Do not use the unquoted form for secrets: the value is parsed as YAML, so `yes` becomes
`true` and `pass #word` silently becomes `pass`. When its variable is unset or empty, the unquoted form becomes null:
a scalar key fails at boot, with or without a default, unless its schema allows null; a list becomes `[]`; a struct
keeps its field defaults and fails if a field has none; a key without a schema stays null. This is the same on v3
and v4.

**Can I keep `.cue` files that work on both v3 and v4?**
Yes, as long as they use only dual-valid syntax (step 3), do not reference definitions, and have no reference that
the `.cue` merge leaves pointing at a dropped declaration (see [Errors and fixes](#errors-and-fixes)).

**Can v3 and v4 instances run side by side?**
Configuration-wise, yes, as long as they do not share converted `.cue` files. YAML is read the same by both, as long
as `flamingo.modules.disabled` entries keep their v3 paths (step 4b). Sessions are not shared: stored session values
carry Flamingo's module path, so a user who moves between a v3 and a v4 instance, or is caught by a rollback, gets a
new session. See the v4 release notes.

**Can I use `package flamingo` in my `.cue` files?**
Do not add one. In the first user file (usually `config.cue`), v4 accepts `package flamingo` and v3 rejects it; any
other name fails on v3 and, in most layouts, on v4. In later files (`config_<context>.cue`, `config_local.cue`,
`CONTEXTFILE` files) both versions ignore package clauses.

**Why not upgrade CUE in smaller steps?**
No intermediate CUE version avoids the breaking changes, and CUE has no compatibility mode. The snapshot comparison
is the safety net instead.

**Should I set `CUE_EXPERIMENT` or `CUE_DEBUG`?**
No. v4 ignores `CUE_DEBUG`, and in CUE v0.17.1 `CUE_EXPERIMENT` changes nothing Flamingo uses.
