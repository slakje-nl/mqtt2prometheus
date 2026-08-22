# CLAUDE.md

Guidance for Claude Code when working in this repository.

mqtt2prometheus subscribes to MQTT topics and exposes selected messages as Prometheus metrics.
The mapping from topic to metric lives entirely in config, never in Go.

Repository: `git@github.com:slakje-nl/mqtt2prometheus.git`
Image: `ghcr.io/slakje-nl/mqtt2prometheus`

---

## Development Commands

```bash
just                  # list recipes
just check            # every gate: format, test, security
just build            # compile to bin/mqtt2prometheus
just run              # run against config/ (needs a reachable broker)
just verify           # mqtt2prometheus --verify-config --config config/
just format           # gofmt check + go vet + golangci-lint (read-only, same as CI)
just lint             # apply gofmt + golangci-lint fixes
just test             # unit tests + 100% coverage gate + feature tests (requires Docker)
just test-unit        # unit tests + coverage gate (needs Docker for internal/broker)
just test-feature     # feature tests only (starts a real mosquitto container)
just security         # go mod verify + govulncheck + gosec + gitleaks (same as CI)
just docker-build     # build the image locally
```

**`just format`, `just test` and `just security` MUST all pass before any change is considered
done.** They run the exact same checks as CI.

Always run commands via `mise exec` so the Go version pinned in `mise.toml` is used:

```bash
mise exec -- just format
mise exec -- just test
mise exec -- just security
```

Tooling (`golangci-lint`, `govulncheck`, `gosec`, `gitleaks`) is auto-installed on demand by the
recipes that need it. Don't `go install` manually — if a tool is missing, fix the recipe.

Always run `just lint` after editing files to apply fixes, then verify with `just format`.
Before declaring a change ready: run `just format` and `just test`, run `mcp__ide__getDiagnostics`,
and scan touched files for style violations (missing blank lines after guards, unrelated blocks
touching, unhandled errors).

## Commits

**Verify every commit, but do not wait for approval.** Run `just format && just test` before each
commit so every commit is atomic, green and runnable, and `git bisect` always lands on a working
tree. Do not pause to have each message approved — keep moving and let the user review the branch
as a whole.

Never commit a broken intermediate state and fix it in the next commit.

Conventional commits: `type(scope): description`. Types: `feat`, `fix`, `refactor`, `test`,
`docs`, `chore`, `ci`. Scopes: `config`, `rules`, `store`, `exporter`, `broker`, `app`, `cmd`,
`docker`, `ci`, `docs`. Subject line under 72 characters.

## Deployment

**Nothing deploys from this repository except the image push.** There is no dev environment, no
deploy workflow, no environment-specific secrets. Merging to `main` tags the next integer version
and pushes `ghcr.io/slakje-nl/mqtt2prometheus:vN` and `:latest`. Rolling that out is manual: the
user copies a config directory to their own server and restarts the container.

`config/` in this repository is an **example** configuration with placeholder values. It is
verified by `--verify-config` in CI, which is what stands between a bad rule and a broken
exporter. It is not the user's live config and must never become it — see Security and Privacy.

---

## Security and Privacy

**This repository is public.** Everything committed here is permanent and world-readable,
including anything later "removed" in a follow-up commit. Treat every file, log line and image
label as published the moment it is written.

### Never commit

- **Real hostnames or IPs** of the user's network. No `192.168.*`, no `10.*`, no `*.lan` or
  `*.local` name that actually resolves on their LAN, no Tailscale or VPN addresses. **The broker
  address is a secret like any other**: the example config reads `broker: ${MQTT_BROKER}`, and no
  committed file names a host. Where a literal is genuinely unavoidable in prose, use an
  RFC 5737 documentation address (`192.0.2.10`), never a plausible LAN address.
- **Filesystem paths that reveal the user's own layout.** The standard unraid convention
  `/mnt/user/appdata/mqtt2prometheus` is fine in the README, because it is identical for every
  unraid user. A path naming their shares, drives or directory names is not.
- **Real device or topic names.** Z-Wave node names, zigbee2mqtt friendly names and room names
  describe the user's home. Examples and `testdata/` use `example_sensor`, `example_outlet` and
  `example device` (the space is deliberate — it is a real edge case in topic matching).
- **Credentials of any kind**, including ones that look fake. Everything host- or
  credential-shaped is injected at runtime through `${ENV_VAR}` expansion and appears in no
  committed file: `${MQTT_BROKER}`, `${MQTT_USERNAME}`, `${MQTT_PASSWORD}`. Because every config
  field is required, an unset variable fails `--verify-config` loudly rather than silently
  connecting somewhere unintended.

### Never leak at runtime or in CI

- **Never log a config value that could carry a secret.** Log the config *shape* (source name,
  rule count, subscribe filter), never the resolved `mqtt.password`, and never a full broker URL
  if it carries userinfo. Redact `mqtt.username` and `mqtt.password` in `--verify-config` output
  and in any startup summary.
- **Never log message payloads at `info`.** They contain the user's home telemetry.
- **Never echo an environment variable in a CI step.** No `run: echo $MQTT_PASSWORD`, no `set -x`
  in a step that touches secrets, no `docker build --build-arg` carrying a credential (build args
  are visible in image history).
- **Image labels carry no personal data.** Set
  `org.opencontainers.image.source=https://github.com/slakje-nl/mqtt2prometheus` and
  `org.opencontainers.image.licenses`. Do **not** set `org.opencontainers.image.authors` — it is
  the standard place an email leaks into `docker inspect` and into the GHCR web UI.
- **The image push logs nothing personal.** Login uses the built-in `GITHUB_TOKEN`, never a
  personal access token and never `docker login -u <email>`. There are no registry secrets in this
  repository.
- **`gitleaks` runs in `just security` and in CI**, and its finding is a hard failure. Never add
  an allowlist entry to silence a real finding; fix the file.

### Before pushing a branch

```bash
git log --format='%ae' | sort -u                 # no personal address in authorship
just security                                     # gitleaks clean

# any email address; the repository SSH URL is the only expected hit
grep -rnE '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' . --exclude-dir=.git --exclude=go.sum

# private network addresses; 192.0.2.x is RFC 5737 documentation space and is fine
grep -rnE '192\.168\.|10\.[0-9]{1,3}\.[0-9]{1,3}\.|172\.(1[6-9]|2[0-9]|3[01])\.' . --exclude-dir=.git
```

Both patterns match *any* address rather than a specific one, so this file never names the values
it is protecting. Anything they turn up that is not listed above as expected is a leak.

---

## Architecture

- **Go stdlib `net/http`** on `:9000`, `http.ServeMux`, explicit timeouts, real `Shutdown`.
  Never `http.DefaultServeMux`, never bare `http.ListenAndServe`.
- **`eclipse/paho.golang/autopaho`**, MQTT 5. Chosen over `paho.mqtt.golang` (3.1.1, maintenance
  mode) because subscribe returns a reason code per topic filter, so a rejected `$SYS`
  subscription is detectable rather than silently producing no data.
- **The MQTT client lives behind `internal/broker.Broker`.** No paho type appears outside
  `internal/broker`. This is what makes the 100% coverage gate reachable and lets feature tests
  swap in a real container without touching the pipeline.
- **Never block the broker callback.** The handler puts the message on a buffered per-source
  channel and returns immediately. If the buffer is full the message is dropped and
  `mqtt2prom_messages_dropped_total` increments. A dropped sample is better than a stalled MQTT
  client, and the counter makes the trade visible.
- **One connection per broker URL.** Sources sharing a broker share a connection and a client id;
  their subscriptions are disjoint so nothing is lost. A source declaring its own `broker:` gets
  its own connection and goroutine. QoS is per-subscription, so sources on one connection can
  still differ (`mosquitto` needs QoS 2).
- **One processing goroutine per source**, each with its own buffered channel, fed by a router
  that dispatches on topic. Isolation where it matters, without the connection overhead.
- **A custom `prometheus.Collector` over a sample store, never `promauto` or `GaugeVec`.** Metric
  names come from config and are not known at compile time, and counters need reset detection.
  The store is keyed by name plus label fingerprint; `Collect` walks a snapshot.
- **Counter reset detection**: a counter receiving an absolute value lower than the previous one
  is treated as a source restart. An offset is carried so `rate()` and `increase()` stay correct
  across a broker or device restart.
- **`log/slog`** with a JSON handler, always. There is no console format and no log format knob.
  Never log a message payload at `info`: on a busy `$SYS` subscription that is constant disk
  churn, and payloads carry the user's home telemetry.
- **Config is YAML**, one file for the process and one file per source, with `${ENV_VAR}`
  expansion so credentials never sit in a config file.
- **SIGHUP reloads config** without dropping counter offsets. A rule that disappears drops its
  series; a rule that survives keeps its accumulated offset.

Package layout:

```
cmd/mqtt2prometheus/   flags, signal handling, calls internal/app
internal/config/       schema, glob loading, env expansion, validation
internal/rules/        compiled rule: regex match, label extraction, value extraction
internal/store/        sample store, counter reset detection, snapshots
internal/exporter/     prometheus.Collector over the store, self-metrics
internal/broker/       Broker interface, autopaho implementation, topic router
internal/app/          wiring, per-source goroutines, reload, HTTP server
tests/feature/         //go:build feature, real mosquitto via testcontainers-go
testdata/              golden corpus: captured payloads and expected metrics
config/                the user's real configuration, verified in CI
```

`cmd/` stays thin. Anything with a branch in it belongs in `internal/app` so the coverage gate
reaches it.

**Ask before adding anything that is not the Go standard library.** This covers every kind of
addition, not just `go.mod`: a Go module, a linter or scanner, a `go run` tool in the justfile, a
container base image, a GitHub Action, an npm package in CI, a mise tool. Propose it, say what it
buys and what it costs, and wait. Never add one as a side effect of solving something else.

If the standard library can do the job at reasonable cost, use the standard library.

The approved set, each at its newest stable release:

| | |
|---|---|
| Go modules | `eclipse/paho.golang`, `prometheus/client_golang`, `gopkg.in/yaml.v3`, `stretchr/testify`, `testcontainers-go` |
| Tools, run through `go run` | `golangci-lint`, `govulncheck`, `gosec`, `gitleaks` |
| Toolchain | `mise`, `just` |
| Images | `golang:1.27-alpine` (build), `gcr.io/distroless/static-debian12:nonroot` (runtime), `eclipse-mosquitto:2` (tests) |
| CI | `actions/checkout`, `actions/setup-node`, `jdx/mise-action`, `docker/setup-buildx-action`, `docker/login-action`, `docker/build-push-action`, `@commitlint/cli`, `@commitlint/config-conventional` |

---

## Config Rules

**Every field is required and validated. There are no defaults.** No `defaultXxx` constants, no
`if cfg.X == 0 { cfg.X = ... }` fallbacks, no `CheckAndSetDefaults` helpers. An operator who
omitted a value gets a loud failure from `--verify-config`, never behaviour the config file no
longer documents.

The only optional fields are the ones that are genuinely a feature, not a knob:

| Field | Level | Optional because |
|---|---|---|
| `help` | rule | absent means no `# HELP` line, which is exactly today's behaviour |
| `last_updated_metric` | source, rule | a heartbeat is opt-in |
| `labels` | source, rule | extra labels beyond the regex captures are opt-in |
| `scale`, `regex`, `map` | value | transforms are opt-in |
| `broker` | source | absent means use the process-level broker |

Everything else — `mqtt.broker`, `mqtt.client_id`, `mqtt.username`, `mqtt.password`, `mqtt.qos`,
`mqtt.clean_session`, `server.listen`, `log.level`, `sources`, and every rule's `match`,
`metric_name`, `type`, `value.from` — is required.

**Durations use `time.Duration` syntax** (`5s`, `1m`, `24h`), never an int field named
`*_seconds`.

`--verify-config` must catch, at minimum: unparseable regex, a `metric_name` used twice with
different label sets, a named capture that collides with a declared label, an unknown
`value.from`, a `type` that is neither `gauge` nor `counter`, and a source whose `subscribe`
filter cannot match its own rules' regexes.

---

## Comments

**Do not write comments.** Names and structure carry the meaning: extract a well-named function or
variable instead of explaining one. This includes godoc on exported identifiers when it only
restates the name.

Where a WHY genuinely needs recording — a workaround for an upstream bug, a load-bearing goroutine
ordering, a non-obvious invariant — **put it in the commit message.** That is where reasoning
belongs: it is dated, attributed, and `git blame` finds it from the line itself, whereas a comment
rots silently the moment the code around it changes.

Compiler directives are not comments. `//go:build`, `//go:embed` and `//go:generate` stay.

When you touch a file, delete any comment you find in the lines you changed.

---

## Documentation

- **`CLAUDE.md`** is permanent: conventions, architecture, the config contract, the privacy rules.
  Anything that must still be true in a year lives here.
- **`README.md`** is the only user-facing document: what the tool does, the config schema, a
  compose example, how to add a rule. Operator-facing behaviour goes here, never into source.

Any change that alters externally-visible behaviour — the config schema, metric names or types,
counter reset semantics, reload behaviour, self-metrics, the image contract — MUST update
`README.md` in the same commit.

Do not create other doc files without being asked.

---

## Code Style

- **Newest stable Go**, currently 1.27, pinned in `mise.toml`, which is the source of truth.
  Standard library plus the five dependencies listed above.
- **Every dependency sits at its newest stable release.** Pin exact versions in `go.mod` at
  scaffold time and let Dependabot move them. Never adopt a pre-release or a pseudo-version.
- **Idiomatic Go** — accept interfaces, return structs; table-driven tests with `t.Run`; errors as
  values, never panic in library code; zero-value usability; small focused interfaces; name things
  the Go way (`nodeID` not `nodeId`, `HTTPServer` not `HttpServer`, receiver names are one-letter
  abbreviations of the type).
- Add a blank line after each `return` or early-return guard.
- Add a blank line between logically unrelated blocks within a function.
- **No `//nolint` directives anywhere.**
- **No coverage exclusion comments.** `main()` and signal handlers are excluded by test scope
  (`./internal/...`), not by annotation.
- **Never exclude a linter or scanner rule to clear a finding.** Fix the code first. The only
  acceptable exclusion is a rule that is broken in the upstream tool and genuinely cannot be
  satisfied by changing our code. When excluding, the config must carry a comment linking the
  upstream bug and describing the in-code mitigation, so it can be revisited.
- **Never ignore returned errors** in non-test code — no `_ = someFunc()`; always handle or log.
- Prefer explicit error handling over panics; `log.Fatal` only in `main()`.
- **NEVER hardcode credentials** — always read from environment via config.
- Follow existing patterns for new components.

---

## Testing Philosophy

- **Never mock internal functions** — mock at the boundary. Mock `broker.Broker`, never a function
  inside `internal/rules`.
- **Never mock paho** — feature tests use a real mosquitto container via testcontainers-go.
- **Every test must assert something.** A test that calls a function without checking output or
  side effects is not a test; delete it.
- **Use `testify/require` for all assertions** — never `t.Errorf` / `t.Fatalf`.
- **Never put the tested call inside `require.*`** — assign first, then assert:
  `err := rule.Match(topic); require.NoError(t, err)`, not `require.NoError(t, rule.Match(topic))`.
- **Keep interfaces small from the start.** Accept the narrowest interface the consumer actually
  needs. Compose narrow interfaces into a wider one only at the wiring layer.
- **Mocks live inline in the `*_test.go` that uses them.** Cross-package fakes are a smell.
- **No coverage exclusions** on anything exercisable by a test.
- **`testdata/` holds the captured topics and payloads** from the three old exporters plus the
  metrics each must produce. It is driven by the feature tests. Adding a rule to `config/` without
  adding its case is incomplete work.

## Test Structure

Two layers. Unit tests cover logic, feature tests cover everything end to end. There is no middle
layer.

**Coverage rule:** unit tests own logic coverage. The 100% gate on `./internal/...` exists to
force every branch and error path into a unit test. If a logic branch isn't unit-reachable,
refactor it — don't push it into a feature test. Feature tests aren't where coverage gets earned.

### Unit tests (`internal/*/**_test.go`)

- Basic scenarios of pure logic: config parsing and validation, regex compilation, label
  extraction, JSON path extraction, value transforms, counter reset arithmetic, store snapshots,
  Collector output.
- No network and no paho, mocked at `broker.Broker`, with one exception: `internal/broker` starts
  a real `eclipse-mosquitto` container, because the one line handing autopaho's concrete
  connection manager to our own code cannot be reached any other way. That means `just test-unit`
  needs Docker.
- One representative case per branch, table-driven with `t.Run`. Exhaustive payload coverage is
  the feature tests' job, not this layer's.
- Coverage on `./internal/...` is gated to 100%.

### Feature tests (`tests/feature/`, `//go:build feature`)

- Full scenarios: real mosquitto container via testcontainers-go, real binary, real subscriptions,
  the real `config/`, and every captured payload in `testdata/`.
- This layer is the migration contract. It is what proves each of the 32 metric families comes out
  with the right name, labels, type and value.
- One test tells a complete story: start broker, start exporter, publish, scrape `/metrics`,
  assert the exposition text.
- Cover: QoS 1 and 2, a `$SYS` topic containing a space, a device name containing a space, broker
  restart and reconnect, SIGHUP reload preserving a counter offset.
- Use inline labels for each **logical scenario step** (the user-visible action: "publish a meter
  reading", "restart the broker"). Do not label technical mechanics. Skip the test-level
  docstring; the test name carries the intent.
- Feature tests run after the coverage gate and do not contribute to it.

---

## Branch Flow & Release

- **feature branches** — work happens here, PR targets `main`.
- **`main`** — protected, rebase-and-merge only. Every push runs `release.yml`, which computes the
  next integer tag from existing `v*` tags, tags the commit, and pushes
  `ghcr.io/slakje-nl/mqtt2prometheus:vN` and `:latest`.
- CI on every PR: `format`, `test` (unit + coverage gate + feature), `security`, `verify-config`
  against `config/`, and a Docker build that is not pushed.
- Dependabot weekly for `gomod`, `docker` and `github-actions`, grouped, with scoped commit
  prefixes.

Image publishing uses the built-in `GITHUB_TOKEN` with `permissions: {packages: write}`. There are
no registry secrets in this repository.
