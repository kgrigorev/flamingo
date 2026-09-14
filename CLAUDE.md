# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Flamingo is a Go web framework (module path `flamingo.me/flamingo/v3`) built around the Dingo dependency-injection container (`flamingo.me/dingo`). It is a library, not an application: there is no binary to build here. `examples/hello-world/main.go` is the smallest runnable consumer.

## Commands

Go version comes from `go.mod` (`toolchain` directive); `GOTOOLCHAIN=auto` downloads it if needed.

```bash
# Tests (CI runs these with the race detector; CGO is required for -race)
CGO_ENABLED=1 go test -race ./...

# Single package / single test
go test ./framework/config
go test ./framework/web -run 'TestRouter.*' -v

# Lint (golangci-lint v2; version pinned in .github/workflows/golangci-lint.yml)
golangci-lint run ./...

# Static checks CI enforces; all must produce no diff/output
go vet ./...
gofmt -l .
go run golang.org/x/tools/cmd/goimports@latest -w . && git diff --quiet
go generate ./... && git diff --quiet

# Regenerate mocks (mockery is a go.mod tool; config in .mockery.yml)
go generate ./...
```

Note: the `Makefile` `test` target is stale (calls `golint`); use the commands above, which mirror `.github/workflows/main.yml`.

## Conventions that CI and lint actually enforce

- **golangci-lint is strict** (`.golangci.yml`): `wsl_v5` whitespace rules, `varnamelen`, `mnd` (no magic numbers), `err113` (no dynamic errors; declare sentinel `var Err... = errors.New(...)` and wrap with `%w`), `wrapcheck`, `forcetypeassert` (always `v, ok := x.(T)`), `paralleltest`/`tparallel`, `testpackage` (tests use `package foo_test`), `thelper`, `nolintlint` (every `//nolint` needs a specific linter and an explanation). CI runs with `only-new-issues: true`, so only lines you touch are checked, but new code must be clean.
- **Code generation**: always `//go:generate go tool <name>` (tools live in `go.mod` via `go get -tool`), never a bare tool name. The only generate directive is in `app.go` and runs mockery over the interfaces listed in `.mockery.yml`; add new interfaces there, mocks land in `<pkg>/mocks/`.
- **Dingo injection**: prefer `Inject(...)` methods over `inject:""` tags on exported fields. Config values are injected through anonymous structs with `inject:"config:some.key"` tags (see `servemodule.Inject` in `app.go`).
- **Context** is always the first parameter; never pass nil contexts.
- **Commit messages**: Conventional Commits with the module as scope, e.g. `fix(framework/web): ...`, `feat(core/auth): ...`. Semanticore generates `CHANGELOG.md` and releases from these on master; do not edit `CHANGELOG.md` by hand.
- Every module directory has a `Readme.md` starting with an h1 title; docs in `docs/` are numbered markdown rendered to docs.flamingo.me.
- `framework/testutil` (PACT) and `core/cache` are deprecated; do not extend them.

## Architecture

### Bootstrap (`app.go`)

`flamingo.App(modules, options...)` → `NewApplication` does everything in order:

1. Parses framework flags (`-flamingo-context`, `-flamingo-config`, `-flamingo-config-cue-debug`, `-dingo-inspect`, ...).
2. Prepends the always-on modules: `framework.InitModule`, the logger module (`core/zap` by default, replaceable via `WithCustomLogger`), `core/runtime`, `framework/cmd`; appends the internal `servemodule` (the `serve` cobra command and HTTP server lifecycle).
3. Builds a `config.Area` tree (`config.NewArea("root", modules, childAreas...)`) and calls `config.Load`.
4. Flattens areas, picks the one named by `-flamingo-context` (default `root`), and gets its initialized Dingo injector.
5. `Run()` resolves the root `*cobra.Command` (annotated `"flamingo"`), dispatches `flamingo.StartupEvent`, and executes it. Every CLI subcommand (`serve`, `config`, `routes`, `handler`, `modules`, `version`, ...) is a `BindMulti(new(cobra.Command))` contribution from some module.

### Modules and config areas (`framework/config`)

A Flamingo module is any `dingo.Module` (has `Configure(*dingo.Injector)`). Optional interfaces the framework detects at load time (`framework/config/area.go`):

- `Depends() []dingo.Module` – transitively pulls in required modules (deduplicated by type).
- `CueConfig() string` – a CUE schema fragment declaring the module's config keys with defaults (`key: type | *default`). All module schemas are unified with the YAML config files, so a key missing from every schema is a load error. This is the mechanism for "default configuration".
- `FlamingoLegacyConfigAlias() map[string]string` – maps old flat keys to new ones.

Config areas form a tree; children inherit modules and config from the parent and are used for per-site/per-locale setups (usually with `framework/prefixrouter`). Loading order and file names (`config.yml`, `routes.yml`, `config_$CONTEXT.yml`, `config_local.yml`, `CONTEXTFILE`, `--flamingo-config`) are documented in `framework/config/Readme.md`.

### Web layer (`framework/web`)

- Routes are registered by types implementing `web.RoutesModule` (`Routes(*web.RouterRegistry)`), bound with `web.BindRoutes(injector, m)` inside a module's `Configure`. The registry maps paths to named handlers (`registry.Route("/path", "name")` + `registry.HandleAny/HandleGet/HandleData("name", action)`), which enables reverse routing by name.
- Handler signature: `func(ctx context.Context, req *web.Request) web.Result`. `web.Result` implementations live in `result.go`; `web.Responder` (bound in the framework) is the usual way to produce them. `HandleData` registers data controllers exposed at `/_flamingo/json/{handler}` and callable from templates via `get(...)`.
- Cross-cutting behaviour is done with `web.Filter` (`BindMulti(new(web.Filter))`), a chain around every request. Sessions are gorilla-based and accessed via `web.SessionFromContext` / `req.Session()`.
- `framework/flamingo` holds the shared abstractions everything else depends on: `Logger`, `EventRouter`/`Event` (startup, server start/shutdown, request events; subscribe with `flamingo.BindEventSubscriber`), `TemplateEngine`, and `BindTemplateFunc` for template helpers.

### Layout

- `framework/` – the kernel: `config`, `web`, `flamingo`, `cmd`, `controller` (default render/redirect/error/static handlers), `http`, `opencensus` (tracing/metrics; note the module also depends on OpenTelemetry), `prefixrouter`, `systemendpoint` (separate ops HTTP port for metrics/healthchecks).
- `core/` – optional batteries: `auth` (current, pluggable identifiers; `oauth` is the legacy predecessor), `security`, `locale`, `gotemplate`, `healthcheck`, `requestlogger`, `requesttask`, `robotstxt`, `zap`/`silentzap`, `runtime`, `internalauth`.
- Business-logic modules follow ports-and-adapters: `domain/`, `application/`, `interfaces/` (controllers, template functions), `infrastructure/` (adapters), with `module.go` at the root (see `docs/1. Flamingo Basics/3. Flamingo Module Structure.md` and `core/security` as a reference).
