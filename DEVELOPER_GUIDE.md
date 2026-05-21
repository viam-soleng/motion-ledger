# Developer guide

For end-user setup, see [SETUP.md](SETUP.md). For configuration reference,
see [README.md](README.md). This document is for people **modifying** the
module.

## Project layout

```
.
├── module.go                       # resource registration, Config, lifecycle, DoCommand dispatch
├── module_test.go                  # unit tests for Validate + applyDefaults
├── cmd/
│   └── module/main.go              # entrypoint — calls module.ModularMain
├── utils/
│   ├── ledger/                     # pure ledger storage (LoadOrCreate, Prune, WriteAtomic, ...)
│   │   ├── ledger.go
│   │   └── ledger_test.go
│   └── motion/                     # vision-service detector resolution + QueryMotion
│       └── motion.go
├── meta.json                       # Viam module manifest
├── Makefile                        # build, test, lint, module.tar.gz
├── README.md                       # configuration reference
├── SETUP.md                        # end-user setup walkthrough
└── viam-soleng_motion-ledger_ledger.md    # registry model page
```

The split is deliberate: `utils/ledger/` has no Viam dependencies and is
fully unit-testable; `utils/motion/` is the only place that talks to the
`vision.Service` API; `module.go` is glue + lifecycle + locking.

## Local development

```sh
make setup        # go mod tidy
go build ./...    # confirm everything compiles
go vet ./...      # static analysis — must be clean
go test ./...     # unit tests (utils/ledger + root package)
make lint         # gofmt -s -w .
make module       # produces module.tar.gz (linux/<host arch> by default)
```

`go vet` will print a few `interface{} can be replaced by any` style hints
on the existing code — those are pre-existing and not regressions. The
build passes when stdout from `go vet` is empty *of errors* (style hints
go to a different stream depending on your toolchain).

### Cross-compiling for a Raspberry Pi

The RDK transitively imports `pion/mediadevices` which needs CGO. To
cross-compile for `linux/arm64` from a non-Linux host without a C
toolchain, strip the CGO-backed paths with `-tags no_cgo`:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -tags no_cgo \
  -o bin/motion-ledger \
  ./cmd/module
```

Verify the result is an ARM64 ELF binary:

```sh
file bin/motion-ledger
# expected: ELF 64-bit LSB executable, ARM aarch64, statically linked, ...
```

For other targets, swap `GOARCH=arm64` for `amd64`, etc. `-tags no_cgo` is
only needed when cross-compiling — native builds (e.g., building on the
Pi itself) don't need it.

## Architecture notes

These are the non-obvious bits worth understanding before changing the
code.

### `Validate` mutations don't survive

Viam's runtime parses the JSON config twice: once to call `Validate` for
dependency declaration, then again via `resource.NativeConfig` to produce
the `*Config` passed to the constructor. **Any field mutation done inside
`Validate` is thrown away** before the constructor runs.

This is why field defaults are applied in `NewLedger` via the
`applyDefaults` helper, not in `Validate`. The earlier version of this
module set defaults inside `Validate` and silently shipped with an empty
`LedgerPath` whenever the operator omitted that field from JSON, which
caused `WriteAtomic` to error with `rename .tmp : no such file or
directory`. The unit tests in `module_test.go` are regression guards for
that bug — if you touch defaults, add tests for the new defaults too.

### `AlwaysRebuild` means no in-place reconfigure

The struct embeds `resource.AlwaysRebuild`. Any config change destroys the
current resource (calling `Close`, which cancels `cancelCtx`) and creates
a new one via the constructor. The polling goroutine is tied to
`cancelCtx`, so it stops cleanly on every reconfigure.

This means you don't need to handle "change `poll_interval_seconds` at
runtime" specially — the whole resource gets rebuilt and the new goroutine
picks up the new interval.

### Polling concurrency

The polling goroutine in `startPolling` calls `handlePollForMotion`
synchronously on each ticker tick. `handlePollForMotion` takes `s.mu`
(write lock), so:

- Manual `DoCommand({"poll_for_motion": true})` calls and internal polls
  serialize against each other.
- If a poll takes longer than `poll_interval_seconds`, the next tick
  fires immediately when the previous one returns. `time.Ticker` drops
  intermediate ticks rather than queuing them.

### Replacing the binary doesn't auto-restart the module process

When viam-server launches a `type: local` module, it execs the binary at
`executable_path`. Replacing the file on disk (e.g., via `scp` + `mv`)
does **not** restart the running process — it keeps using its in-memory
copy. To pick up a new build, restart viam-agent on the target machine:

```
sudo systemctl restart viam-agent
```

This is the most common "I deployed the new binary but nothing changed"
gotcha during development.

## Adding a new DoCommand

`DoCommand` dispatches on top-level keys in the `cmd` map. To add a
command:

1. Add the handler method on `*motionLedgerLedger` in `module.go`. Follow
   the existing pattern — take `s.mu` (or RLock if read-only), do the
   work, return `map[string]interface{}` with at least `"status"`.
2. Add the dispatch case in `DoCommand` (`module.go` ~line 193).
3. Document the command in both `README.md` ("Commands" section) and
   `viam-soleng_motion-ledger_ledger.md` (the table of DoCommand entries).
4. Add a test if the logic is non-trivial.

## Adding a new Config field

1. Add the field to `Config` with a JSON tag.
2. If it has a default, add it to the `applyDefaults` helper, not to
   `Validate`. Test the default in `module_test.go`.
3. If it has structural constraints (must be non-negative, etc.), enforce
   them in `Validate`.
4. Document in `README.md` (attributes table), `viam-soleng_motion-ledger_ledger.md`
   (attributes table), and `SETUP.md` if it changes the setup flow.

## Releasing a new version to the registry

The module's `meta.json` declares a cloud-build configuration that lets
Viam's build runners compile the module for every supported architecture.
This is the recommended publish path — it avoids needing a Linux/ARM
toolchain locally.

### Prerequisites

1. **`viam` CLI installed and authenticated.**
   ```sh
   viam version
   viam auth print-access-token | head -c 40 && echo …   # confirm a token comes back
   ```
   If not installed: `brew install viamrobotics/brews/viam-server` (or
   see [docs.viam.com/cli](https://docs.viam.com/cli/)). If not
   authenticated: `viam login`.

2. **The git ref you want to publish is pushed to GitHub.** The cloud
   builder clones from `meta.json`'s `url` field at the ref you pass via
   `--ref` (default `main`). If `url` is empty, set it:
   ```json
   "url": "https://github.com/viam-soleng/motion-ledger",
   ```
   then commit + push.

3. **Tests pass locally:** `go test ./...` clean.

### Publish flow

Pick a [semver](https://semver.org/) version number. For the first
publish, use `0.1.0`. For subsequent releases:

- **patch** bump (`0.1.0` → `0.1.1`): bug fixes, no behavior changes
- **minor** bump (`0.1.0` → `0.2.0`): new fields or commands, backward
  compatible
- **major** bump (`0.1.0` → `1.0.0`): breaking config changes

Then:

```sh
# Start a cloud build for every arch in meta.json's build.arch
viam module build start --version=0.1.0

# Returns a build ID. Monitor it:
viam module build list
viam module build logs <build-id>
```

The cloud builder runs `make module.tar.gz` for each platform in
`meta.json` `build.arch` and uploads the resulting tarball as a new
version. When the build status is `Done` for all platforms, the version
is live in the registry and any machine using `version: "latest"` (or
that exact version) will pull it on next reconfigure.

If only `meta.json` metadata changed (description, README link, etc.)
and you don't need a new build, push the metadata update on its own:

```sh
viam module update
```

### Publishing from a feature branch

If your changes aren't on `main` yet, pass `--ref`:

```sh
viam module build start --version=0.2.0-rc.1 --ref=claude/confident-curran-710e5c
```

Useful for pre-release testing. Use a pre-release suffix in the version
(`-rc.1`, `-beta.2`, etc.) so it doesn't get picked up by clients using
`latest`.

### Local build + manual upload (fallback)

If cloud build isn't an option, you can build each platform locally and
upload one at a time:

```sh
# Example: linux/arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags no_cgo \
  -o bin/motion-ledger ./cmd/module
tar czf module-linux-arm64.tar.gz meta.json bin/motion-ledger
viam module upload --version=0.1.0 --platform=linux/arm64 \
  --upload=./module-linux-arm64.tar.gz
```

Repeat with `GOARCH=amd64` and `--platform=linux/amd64`, etc. The
`--platform` value must match `build.arch` entries in `meta.json` for the
version to be reachable.

### Verifying a release

After the build is `Done`, on a test machine, point a module entry at the
new version and confirm it loads:

```json
{
  "type": "registry",
  "name": "viam-soleng_motion-ledger",
  "module_id": "viam-soleng:motion-ledger",
  "version": "0.1.0"
}
```

Watch the **LOGS** tab for `Successfully constructed resource ...
viam-soleng:motion-ledger:ledger`. If there's a problem, the customer-facing
errors are documented in [SETUP.md](SETUP.md#troubleshooting).

### Pre-release checklist

Before tagging a new version, confirm:

- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] `go build ./...` clean
- [ ] README and SETUP reflect the changes
- [ ] If `Config` changed, all three doc surfaces are updated:
      `README.md`, `viam-soleng_motion-ledger_ledger.md`, `SETUP.md`
- [ ] meta.json `description` and `url` are accurate
- [ ] Commits are pushed to the ref you'll pass to `--ref`
