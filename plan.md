# plan.md

Working document. History of how this design was reached, the design itself, and the commit
sequence to build it. **Delete this file once the implementation lands** — anything that must
survive belongs in `CLAUDE.md` or `README.md`.

Status: design complete, no code yet. Next step is commit 1.

---

# Part 1 — History

## Why this project exists

This binary plus a set of YAML rule files replaces three hardcoded exporters:
`zwavejs-prometheus-exporter`, `zigbee2mqtt-prometheus-exporter` and
`mosquitto-prometheus-exporter`. All three live under `sokoli-media` and do the same job with
different lookup tables:

| | zwavejs | zigbee2mqtt | mosquitto |
|---|---:|---:|---:|
| Go lines | 406 | 248 | 402 |
| `main.go`, `config.go`, `http.go` | identical | identical | identical |
| lines actually about the domain | ~150 | ~100 | ~150 |

Roughly 70% of the code is triplicated plumbing: connect, subscribe, read a channel, log, shut
down. What differs is a `switch` over topic strings. Three repos, three pipelines, three images,
three containers, to hold three lookup tables.

The deeper cost is that the tables are compiled in. Adding one sensor means editing Go, opening a
PR, waiting for a build, pulling an image and restarting a container. The fact that Z-Wave meter
value id `66049` means watts is a fact about Z-Wave, not about the program.

`hikhvar/mqtt2prometheus` already exists and was rejected for three concrete reasons: it has a
single `topic_path` and a single `device_id_regex` yielding exactly one label, so Z-Wave meters
needing `meter` plus `endpoint` plus a metric name from a numeric segment do not fit; one instance
serves one `topic_path`, so the three sources cannot share a process; and `$SYS/broker/uptime`
publishes `9284 seconds`, a non-JSON scalar needing regex extraction, which it has no story for.

## Defects in the old code, not to be carried forward

- `wg.Done()` with no matching `wg.Add(1)`, so `wg.Wait()` returns immediately and shutdown waits
  for nothing.
- Unbuffered `choke` channel fed from paho's default publish handler, which runs on paho's receive
  goroutine. Any stall blocks the whole MQTT client, and a `quit` arriving during a blocked send
  deadlocks shutdown.
- No `OnConnectionLost` / `OnConnect` handler and no connectivity metric. A broker outage is
  invisible except as missing data.
- `http.ListenAndServe` on the global `DefaultServeMux`, no timeouts, no `Shutdown`. `os.Exit(1)`
  skips every deferred call.
- Gauges never expire, so a dead sensor reports its last value forever.
- `*_unknown_topic{topic}` takes a label value from broker traffic, an unbounded label.
- `mosquitto_received_messages_total` is a Gauge whose name ends in `_total`.
- Inconsistent unit suffixes: `zwave_power_meter_power_watts` against `zigbee_power_meter_power`.
- Every MQTT message logged at `info` with full payload.
- zigbee2mqtt is all-or-nothing: unless `current`, `energy`, `power` and `voltage` are all
  present, the message is discarded. Non-power devices export nothing. `linkquality` is parsed
  into the struct and never used.
- Dockerfile runs on unpinned `debian:latest`, as root, no healthcheck, amd64 only, no version
  stamped in, stray `CMD` in the build stage.
- CI gates nothing: tests run on `branches-ignore: [main]`, the image publishes on push to `main`.
  The image that ships is the one never tested, and only as `:latest`, so there is nothing to roll
  back to.

Worth keeping: `slog` JSON logging, the `_last_update` heartbeat idea, and above all the test
style of asserting real captured payloads against metric values. Those payloads become
`testdata/`.

## The migration contract

Every metric the three exporters produce. Device names here are anonymised placeholders; the
shapes are exactly as captured. 31 metric families: 16 reproduced unchanged, 13 mosquitto ones
renamed to follow their topic paths, and 2 dropped on purpose. The new config produces 32
families from 28 rules.

### zwave-js, subscribed as `zwave/#`

| Topic | Example payload | Metric |
|---|---|---|
| `zwave/example_sensor/lastActive` | `{"time":1711922310802,"value":1711922310552}` | `zwave_node_last_active{node}` = value / 1000 |
| `zwave/example_sensor/sensor_multilevel/endpoint_0/Air_temperature` | `{"time":1735855076246,"value":25.5}` | `zwave_sensor_temperature{sensor}` |
| `.../endpoint_0/Humidity` | `{"time":1735855076298,"value":30}` | `zwave_sensor_humidity{sensor}` |
| `.../endpoint_0/Illuminance` | `{"time":1735855077071,"value":34}` | `zwave_sensor_illuminance{sensor}` |
| `zwave/example_outlet/meter/endpoint_0/value/65537` | `{"time":1735906852206,"value":1}` | `zwave_power_meter_total_consumption_kwh{meter,endpoint}` |
| `.../endpoint_0/value/66049` | `{"time":1735906853203,"value":3.395}` | `zwave_power_meter_power_watts{meter,endpoint}` |
| `.../endpoint_0/value/66561` | `{"time":1735906854203,"value":240.71}` | `zwave_power_meter_voltage_volts{meter,endpoint}` |
| `.../endpoint_0/value/66817` | `{"time":1735906855204,"value":0.023}` | `zwave_power_meter_current_amps{meter,endpoint}` |
| any matched topic | any | `zwave_last_update` |
| `.../ThisMetricDoesntExist`, `.../value/DOESNTEXIST` | any | nothing |

Any endpoint number is accepted for meters. `endpoint_1` was captured as well as `endpoint_0`.

The value ids decode as `(meterType << 16) | (scale << 8) | rateType`, Z-Wave Meter command class:

| Value id | Hex | meterType | scale | rateType | Meaning |
|---:|---|---|---|---|---|
| 65537 | `0x010001` | 1 Electric | 0 | 1 Consumed | kWh |
| 66049 | `0x010201` | 1 Electric | 2 | 1 Consumed | W |
| 66561 | `0x010401` | 1 Electric | 4 | 1 Consumed | V |
| 66817 | `0x010501` | 1 Electric | 5 | 1 Consumed | A |

This decoding is inferred from the four observed ids plus the published Meter CC scale table. It
is consistent across all four but has not been checked against zwave-js source. It is not relied
on by the config, which maps the four ids literally.

### zigbee2mqtt, subscribed as `zigbee2mqtt/#`

Matched shape is `zigbee2mqtt/<device>`, one segment. Captured payload of `zigbee2mqtt/example device`:

```json
{"current":0.54,"energy":1.05,"identify":null,"linkquality":255,"power":102,
 "power_on_behavior":"on","state":"ON",
 "update":{"installed_version":33816645,"latest_version":33816645,"state":"idle"},
 "voltage":239.1}
```

| Metric | Source |
|---|---|
| `zigbee_power_meter_current{device}` | `current` |
| `zigbee_power_meter_energy_total{device}` | `energy` |
| `zigbee_power_meter_power{device}` | `power` |
| `zigbee_power_meter_voltage{device}` | `voltage` |
| `zigbee_power_meter_last_update{device}` | receipt time |
| `zigbee_last_update` | receipt time |
| `zigbee_unknown_topic{topic}` | **dropped** |

The device name contains a space, which is why topic matching must not assume otherwise.
A payload of `{"current":22222.0}` produced nothing under the old all-or-nothing rule.

### mosquitto, subscribed as `$SYS/#`

Scalar payloads, not JSON.

| Topic | Payload | Metric |
|---|---|---|
| `$SYS/broker/uptime` | `9284 seconds` | `mosquitto_uptime` via `^([0-9]+) seconds$` |
| `$SYS/broker/clients/connected` | `2` | `mosquitto_connected_clients` |
| `$SYS/broker/clients/disconnected` | `2` | `mosquitto_disconnected_clients` |
| `$SYS/broker/clients/total` | `5` | `mosquitto_total_clients` |
| `$SYS/broker/clients/expired` | `5` | `mosquitto_expired_clients` |
| `$SYS/broker/subscriptions/count` | `10` | `mosquitto_active_subscriptions` |
| `$SYS/broker/retained messages/count` | `10` | `mosquitto_retained_messages` |
| `$SYS/broker/store/messages/count` | `5` | `mosquitto_stored_messages` |
| `$SYS/broker/store/messages/bytes` | `5` | `mosquitto_stored_bytes` |
| `$SYS/broker/messages/received` | `123` | `mosquitto_received_messages_total` |
| `$SYS/broker/messages/sent` | `123` | `mosquitto_sent_messages_total` |
| `$SYS/broker/bytes/received` | `234` | `mosquitto_received_bytes_total` |
| `$SYS/broker/bytes/sent` | `234` | `mosquitto_sent_bytes_total` |
| any matched topic | any | `mosquitto_last_update` |
| anything else | any | `mosquitto_unknown_topic{topic}` — **dropped** |

`retained messages` contains a space in the topic itself. The old code carried a hardcoded ignore
list (`publish/`, `load/`, `version`, `clients/active`, `clients/inactive`, `clients/maximum`,
`publish/messages/received`, `publish/messages/sent`, `messages/stored`,
`shared_subscriptions/count`) which the new design replaces with "no rule matches, nothing
happens".

## Decisions taken, and why

| Decision | Choice | Why |
|---|---|---|
| One tool or three | One binary, config-driven | The three programs differ only in lookup tables |
| Topic matching | Go regex with named groups | Any pattern expressible; `SubexpNames` turns captures into labels for free |
| Metric naming | Literal `metric_name`, one rule per metric | A `metric_name_map` was rejected as too many config concepts for 28 rules |
| Labels | Named captures become labels; `labels:` adds or renames | A capture consumed by `labels:` stops auto-emitting, so `(?P<endpoint>endpoint_\d+)` needs no rewriting |
| Value extraction | `{from: json, path: …}` or `{from: raw}`, plus `scale`, `regex`, `map` | Covers all three payload shapes with four keys |
| Heartbeats | `last_updated_metric` at source and rule level | Reproduces all four `*_last_update` metrics exactly, with no renaming |
| Metric type | `type` required, gauge or counter | A silently-wrong type breaks `rate()`; requiring it makes the choice deliberate |
| Which counters | Only the six genuinely cumulative ones | `mosquitto_expired_clients` reverted to gauge: probably cumulative, not certain enough |
| Unknown topics | Dropped entirely | Unbounded label, and the counter was never looked at |
| Staleness | No TTL; keep the `last_update` heartbeats | Already in use and understood; TTL can be added later if flat-lining dead devices annoys |
| Sources | One file per source, connections deduped by broker URL | Subscriptions are disjoint, so three connections to one broker buy nothing |
| QoS | Per-source, global default 1, mosquitto 2 | QoS is per-subscription in MQTT, so one connection can still serve both. Mosquitto did not honour 1 |
| Config defaults | None; every field required | An operator who omitted a value gets a loud failure, not undocumented behaviour |
| Optional fields | `help`, `last_updated_metric`, `labels`, `scale`/`regex`/`map`, source `broker` | These are features, not knobs |
| MQTT library | `eclipse/paho.golang` (MQTT 5) | Actively developed; subscribe returns a reason code per filter, so a rejected `$SYS` subscription is detectable |
| Sensor endpoints | Generalised to any endpoint | Adds an `endpoint` label to the three sensor metrics; duplicate series during migration accepted |
| Testing | 100% on `./internal/...` plus a real mosquitto container | Forces the broker behind an interface from commit one |
| Dev stack | None; containers only in tests | Leanest repo |
| Registry | `ghcr.io/slakje-nl/mqtt2prometheus` | Same org as the repo, no registry secrets, no pull limits |
| Workflow | Commit freely, verify each commit, review the branch | Speed over per-commit approval |
| `$SYS` naming | Renamed after the topic paths, varying segment captured as a label | `mosquitto_clients{state}` beats four hand-written names, and the capture-to-label rule gives it for free |
| `$SYS` coverage | Six previously ignored topics added | `publish/messages/dropped` in particular is a real health signal that was being thrown away |
| Automatic resolution | Rejected outright | Generated names would not match anything charted, and a rename map puts back the complexity `metric_name_map` was dropped for |
| Privacy | Repo is public; broker host, credentials and device names never committed | See CLAUDE.md, Security and Privacy |

## Behaviour changes against the old exporters

1. **zigbee2mqtt stops being all-or-nothing.** Rules are independent, so a device publishing only
   `power` exports `zigbee_power_meter_power` instead of nothing.
2. **Heartbeats fire only on a match.** The old zwave and zigbee ones also fired on messages
   nothing understood.
3. **Six metrics become counters** without changing name: `zwave_power_meter_total_consumption_kwh`,
   `zigbee_power_meter_energy_total`, `mosquitto_received_messages_total`,
   `mosquitto_sent_messages_total`, `mosquitto_received_bytes_total`, `mosquitto_sent_bytes_total`.
4. **The three `zwave_sensor_*` metrics gain an `endpoint` label**, so they are new series in
   Prometheus. Duplicate series during migration is accepted.
5. **Two new metrics**: `zigbee_link_quality` and `zigbee_state`.
6. **`zigbee_unknown_topic` and `mosquitto_unknown_topic` are gone.**
7. **Every mosquitto metric is renamed** to follow its topic path, with the varying segment as a
   label: `mosquitto_clients{state}`, `mosquitto_messages_total{direction}`,
   `mosquitto_bytes_total{direction}`. See the mapping table in Part 2. Six `$SYS` topics that
   were previously ignored are now exported.

---

# Part 2 — The design

## Config schema

```yaml
- match: <Go regex with named groups>
  metric_name: <literal string>
  type: gauge | counter
  help: <string>                    # optional
  value: <extractor>
  last_updated_metric: <string>     # optional
  labels: {<name>: <value>}         # optional
```

- Every named group in `match` becomes a label of the same name.
  `(?P<endpoint>endpoint_\d+)` captures `endpoint_0` whole, so the capture is already the label
  value.
- A capture referenced by a `labels:` entry stops being a label on its own.
- `value` is one of `{from: json, path: X}` or `{from: raw}`, with optional `scale`, `regex`
  (group 1 is the value) and `map` (string to number; unmapped means no sample).
- `last_updated_metric` at rule level carries the rule's labels; at source level it carries the
  source's labels, usually none. Both may be set and both are emitted.
- All matching rules run, in file order. Six rules sharing one regex produce six metrics from one
  message.
- A message no rule matches produces nothing: no log line, no counter.
- Sources sharing a broker URL share one connection.

28 rules across three sources produce 32 metric families.

## Example configuration

Committed as `config/`, verified by `--verify-config` in CI. Placeholder values only.

### config/mqtt2prometheus.yaml

```yaml
mqtt:
  broker: ${MQTT_BROKER}
  client_id: mqtt2prometheus
  username: ${MQTT_USERNAME}
  password: ${MQTT_PASSWORD}
  qos: 1
  clean_session: false

server:
  listen: ":9000"

log:
  level: info

sources: sources/*.yaml
```

### config/sources/zwave.yaml

```yaml
name: zwave
subscribe: 'zwave/#'
last_updated_metric: zwave_last_update

rules:
  - match: '^zwave/(?P<node>[^/]+)/lastActive$'
    metric_name: zwave_node_last_active
    type: gauge
    value: {from: json, path: value, scale: 0.001}

  - match: '^zwave/(?P<sensor>[^/]+)/sensor_multilevel/(?P<endpoint>endpoint_\d+)/Air_temperature$'
    metric_name: zwave_sensor_temperature
    type: gauge
    value: {from: json, path: value}

  - match: '^zwave/(?P<sensor>[^/]+)/sensor_multilevel/(?P<endpoint>endpoint_\d+)/Humidity$'
    metric_name: zwave_sensor_humidity
    type: gauge
    value: {from: json, path: value}

  - match: '^zwave/(?P<sensor>[^/]+)/sensor_multilevel/(?P<endpoint>endpoint_\d+)/Illuminance$'
    metric_name: zwave_sensor_illuminance
    type: gauge
    value: {from: json, path: value}

  - match: '^zwave/(?P<meter>[^/]+)/meter/(?P<endpoint>endpoint_\d+)/value/65537$'
    metric_name: zwave_power_meter_total_consumption_kwh
    type: counter
    value: {from: json, path: value}

  - match: '^zwave/(?P<meter>[^/]+)/meter/(?P<endpoint>endpoint_\d+)/value/66049$'
    metric_name: zwave_power_meter_power_watts
    type: gauge
    value: {from: json, path: value}

  - match: '^zwave/(?P<meter>[^/]+)/meter/(?P<endpoint>endpoint_\d+)/value/66561$'
    metric_name: zwave_power_meter_voltage_volts
    type: gauge
    value: {from: json, path: value}

  - match: '^zwave/(?P<meter>[^/]+)/meter/(?P<endpoint>endpoint_\d+)/value/66817$'
    metric_name: zwave_power_meter_current_amps
    type: gauge
    value: {from: json, path: value}
```

### config/sources/zigbee2mqtt.yaml

```yaml
name: zigbee2mqtt
subscribe: 'zigbee2mqtt/#'
last_updated_metric: zigbee_last_update

rules:
  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_power_meter_current
    type: gauge
    value: {from: json, path: current}
    last_updated_metric: zigbee_power_meter_last_update

  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_power_meter_energy_total
    type: counter
    value: {from: json, path: energy}
    last_updated_metric: zigbee_power_meter_last_update

  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_power_meter_power
    type: gauge
    value: {from: json, path: power}
    last_updated_metric: zigbee_power_meter_last_update

  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_power_meter_voltage
    type: gauge
    value: {from: json, path: voltage}
    last_updated_metric: zigbee_power_meter_last_update

  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_link_quality
    type: gauge
    value: {from: json, path: linkquality}

  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_state
    type: gauge
    value:
      from: json
      path: state
      map: {ON: 1, OFF: 0}
```

### config/sources/mosquitto.yaml

Named after the topic paths, with the varying segment captured as a label. `$SYS` topics the rules
do not cover — `load/**`, `version`, the deprecated `clients/active`, `clients/inactive` and
`messages/stored` aliases, and the build-dependent `heap/*` — produce nothing.

```yaml
name: mosquitto
subscribe: '$SYS/#'
qos: 2
last_updated_metric: mosquitto_last_update

rules:
  - match: '^\$SYS/broker/uptime$'
    metric_name: mosquitto_uptime_seconds
    type: gauge
    value: {from: raw, regex: '^([0-9]+) seconds$'}

  - match: '^\$SYS/broker/clients/(?P<state>connected|disconnected|maximum|total)$'
    metric_name: mosquitto_clients
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/clients/expired$'
    metric_name: mosquitto_clients_expired
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/subscriptions/count$'
    metric_name: mosquitto_subscriptions
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/shared_subscriptions/count$'
    metric_name: mosquitto_shared_subscriptions
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/retained messages/count$'
    metric_name: mosquitto_retained_messages
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/store/messages/count$'
    metric_name: mosquitto_store_messages
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/store/messages/bytes$'
    metric_name: mosquitto_store_bytes
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/messages/inflight$'
    metric_name: mosquitto_messages_inflight
    type: gauge
    value: {from: raw}

  - match: '^\$SYS/broker/messages/(?P<direction>received|sent)$'
    metric_name: mosquitto_messages_total
    type: counter
    value: {from: raw}

  - match: '^\$SYS/broker/bytes/(?P<direction>received|sent)$'
    metric_name: mosquitto_bytes_total
    type: counter
    value: {from: raw}

  - match: '^\$SYS/broker/publish/messages/(?P<direction>received|sent)$'
    metric_name: mosquitto_publish_messages_total
    type: counter
    value: {from: raw}

  - match: '^\$SYS/broker/publish/messages/dropped$'
    metric_name: mosquitto_publish_messages_dropped_total
    type: counter
    value: {from: raw}

  - match: '^\$SYS/broker/publish/bytes/(?P<direction>received|sent)$'
    metric_name: mosquitto_publish_bytes_total
    type: counter
    value: {from: raw}
```

Old name to new name, for migrating the dashboards:

| Old | New |
|---|---|
| `mosquitto_uptime` | `mosquitto_uptime_seconds` |
| `mosquitto_connected_clients` | `mosquitto_clients{state="connected"}` |
| `mosquitto_disconnected_clients` | `mosquitto_clients{state="disconnected"}` |
| `mosquitto_total_clients` | `mosquitto_clients{state="total"}` |
| `mosquitto_expired_clients` | `mosquitto_clients_expired` |
| `mosquitto_active_subscriptions` | `mosquitto_subscriptions` |
| `mosquitto_retained_messages` | unchanged |
| `mosquitto_stored_messages` | `mosquitto_store_messages` |
| `mosquitto_stored_bytes` | `mosquitto_store_bytes` |
| `mosquitto_received_messages_total` | `mosquitto_messages_total{direction="received"}` |
| `mosquitto_sent_messages_total` | `mosquitto_messages_total{direction="sent"}` |
| `mosquitto_received_bytes_total` | `mosquitto_bytes_total{direction="received"}` |
| `mosquitto_sent_bytes_total` | `mosquitto_bytes_total{direction="sent"}` |

New, never exported before: `mosquitto_clients{state="maximum"}`,
`mosquitto_shared_subscriptions`, `mosquitto_messages_inflight`,
`mosquitto_publish_messages_total`, `mosquitto_publish_messages_dropped_total`,
`mosquitto_publish_bytes_total`.

### docker-compose.yml

```yaml
services:
  mqtt2prometheus:
    image: ghcr.io/slakje-nl/mqtt2prometheus:latest
    container_name: mqtt2prometheus
    restart: unless-stopped
    ports:
      - '9000:9000'
    volumes:
      - ./config:/config:ro
    environment:
      MQTT_BROKER: ${MQTT_BROKER:?e.g. tcp://192.0.2.10:1883}
      MQTT_USERNAME: ${MQTT_USERNAME:?}
      MQTT_PASSWORD: ${MQTT_PASSWORD:?}
```

## Self-observability

Bounded labels only. `source` takes values fixed by config.

```
mqtt2prom_build_info{version,commit,go_version}          1
mqtt2prom_mqtt_connected                                 1
mqtt2prom_mqtt_reconnects_total                          3
mqtt2prom_messages_received_total{source="zwave"}        1204
mqtt2prom_messages_dropped_total                         0
mqtt2prom_message_errors_total{source="zwave",reason="json"}  2
mqtt2prom_config_reloads_total                           1
mqtt2prom_config_reload_errors_total                     0
```

---

# Part 3 — Implementation plan

One feature branch. Each commit green: `just format && just test` pass before committing.

Two test layers only: unit tests for basic scenarios of pure logic, feature tests for full
end-to-end scenarios. The captured payload corpus lands with the feature tests in commit 15, which
is where the migration contract gets proved.

| # | Commit | Contents | Done when |
|---:|---|---|---|
| 1 | `chore(repo): scaffold module, toolchain and CI` | `go.mod`, `mise.toml`, `justfile`, `.gitignore`, `.dockerignore`, MIT `LICENSE`, README stub, `.github/workflows/ci.yml`, `dependabot.yml` | `just format` and `just test` pass on an empty tree; CI green |
| 2 | `feat(config): schema types and YAML loading` | structs, glob expansion of `sources:`, `${ENV_VAR}` expansion | round-trips the example config; unit tests at 100% |
| 3 | `feat(config): validation with no defaults` | every field required, duplicate `metric_name` with differing label sets rejected, bad regex rejected, unknown `value.from` rejected, capture/label collision rejected | each rejection has a named test with the exact error |
| 4 | `feat(rules): topic matching and label extraction` | compile `match`, captures to labels, consumed-capture rule, source and rule label merge | matches every captured topic, including the one with a space |
| 5 | `feat(rules): value extraction and transforms` | json dotted path, raw float, `regex`, `scale`, `map` | `9284 seconds` to `9284`; `ON` to `1`; `value/1000` |
| 6 | `feat(store): sample store with counter reset detection` | keyed by name plus label fingerprint, offset carried on counter decrease | a counter going 100, 5 reports 105 |
| 7 | `feat(exporter): collector and self-metrics` | `prometheus.Collector` over a store snapshot, `mqtt2prom_*` | exposition text asserted against golden output |
| 8 | `feat(broker): Broker interface and autopaho client` | connect, subscribe with per-filter reason codes, reconnect with backoff, connection dedup by URL | fake at the interface in unit tests; no paho type escapes the package |
| 9 | `feat(app): router and per-source processing` | buffered channel per source, drop counter, dispatch by topic, callback never blocks | a full buffer drops and increments, never blocks |
| 10 | `feat(app): HTTP server` | `/metrics`, `/healthz`, `/readyz`, timeouts, graceful `Shutdown` | shutdown drains within the deadline |
| 11 | `feat(cmd): main, flags and signal handling` | `--config`, `--version`, SIGINT/SIGTERM | binary runs and exits cleanly |
| 12 | `feat(cmd): --verify-config` | resolve and print every source and rule, redact username and password, non-zero exit on any problem | fails on each of the commit-3 rejections |
| 13 | `feat(app): SIGHUP config reload` | swap rules, keep counter offsets for surviving series, drop series whose rule disappeared | offset survives a reload; removed rule's series vanishes |
| 14 | `feat(config): example configuration` | `config/` with the 28 rules above | `just verify` passes; CI runs it |
| 15 | `test(feature): end-to-end against a real broker` | `testdata/` with every captured topic and expected metric; testcontainers mosquitto, real binary, publish the corpus, scrape `/metrics` | all 32 families produced with the right name, labels, type and value; covers QoS 1 and 2, both space-containing names, reconnect, SIGHUP |
| 16 | `feat(docker): distroless image and healthcheck subcommand` | multi-stage, non-root, `-ldflags` version stamp, `HEALTHCHECK`, amd64 and arm64 | image under 15 MB, runs as non-root |
| 17 | `ci: coverage gate, security scans and image publish` | 100% gate, `govulncheck`, `gosec`, `gitleaks`, `--verify-config`, `release.yml` tagging `vN` to GHCR | a merge to `main` produces a pullable `:vN` |
| 18 | `docs: README` | see the section below | a stranger can set it up on unraid without reading Go |

## README.md contents (commit 18)

The README is the only user-facing document. It must open with the disclaimer, because this
project exists as much to demonstrate a way of working as to export metrics.

### Disclaimer, at the very top

State plainly:

- This is a **test project**, built to demonstrate how to work with Claude Code on a real,
  small piece of infrastructure in a safe and reviewable way.
- **Code you did not write, from a software engineer you do not know, should always be verified
  before you run it in your own infrastructure.** That applies to this repository as much as to
  any other. Read the source, read the Dockerfile, check what the container can reach, and decide
  for yourself.
- It is not battle-tested. It runs, it is tested, and it does what it says, but it has not lived
  in production long enough to have earned anyone's trust with their home network.

Keep the tone matter-of-fact, not alarmist, and do not bury it below the feature list.

### Setting it up on unraid

A step-by-step that assumes no Go and no Prometheus expertise:

1. **Prerequisites** — the Docker Compose Manager plugin from Community Applications, a reachable
   MQTT broker, and an existing Prometheus. Say which MQTT versions work (mosquitto 1.6+, since
   the client speaks MQTT 5).
2. **Create the appdata directory** and drop `config/mqtt2prometheus.yaml` plus
   `config/sources/*.yaml` into it. Show the tree.
3. **The compose stack** — add a new stack in the plugin, paste `docker-compose.yml`, and set
   `MQTT_BROKER`, `MQTT_USERNAME` and `MQTT_PASSWORD` in the stack's `.env`. Explain that the
   broker address and credentials never live in the config files.
4. **Least privilege** — the container needs no host networking, no privileged mode, no bind
   mounts other than the read-only config directory, and no access to `/var/run/docker.sock`.
   Say so explicitly, so a reader can compare against what the compose file actually asks for.
5. **Check it works** — `curl http://<unraid-ip>:9000/metrics`, and what a healthy first scrape
   looks like. Mention `--verify-config` for checking a config change before restarting.
6. **Point Prometheus at it** — the `scrape_configs` snippet.
7. **Adding a metric** — take one real topic, write the rule, run `--verify-config`, restart.
   This is the payoff of the whole design and deserves a worked example.
8. **Upgrading and rolling back** — `:vN` tags exist precisely so a bad release is one edit away
   from being undone. Explain that `:latest` moves on every merge to `main`.

### Also in the README

- What it is, in two sentences, above the fold.
- The config schema table from Part 2.
- The full example config.
- The metric list it produces.
- A short note that it replaces three earlier exporters, linking them.
