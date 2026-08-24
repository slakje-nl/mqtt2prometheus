# mqtt2prometheus

Subscribes to MQTT topics and exposes selected messages as Prometheus metrics. The mapping from
topic to metric lives entirely in config, so adding a sensor is a config edit and a restart rather
than a code change and a rebuild.

> [!WARNING]
> **This is a test project.** It was built to demonstrate how to work with Claude Code on a real,
> small piece of home infrastructure in a way that stays safe and reviewable.
>
> **Code you did not write, from a software engineer you do not know, should always be verified
> before you run it in your own infrastructure.** That applies to this repository as much as any
> other. Read the source, read the [`Dockerfile`](Dockerfile), check what the container is allowed
> to reach, and decide for yourself.
>
> It runs and it is tested, but it has not lived in production long enough to have earned anyone's
> trust with their home network.

## What it does

A rule matches a topic with a regular expression, turns the named capture groups into labels, and
pulls a number out of the payload:

```yaml
- match: '^zwave/(?P<meter>[^/]+)/meter/(?P<endpoint>endpoint_\d+)/value/66049$'
  metric_name: zwave_power_meter_power_watts
  type: gauge
  help: Current power draw in watts
  value: {from: json, path: value}
```

```
zwave/example_outlet/meter/endpoint_0/value/66049  {"time":1735906853203,"value":3.395}

  -> zwave_power_meter_power_watts{meter="example_outlet",endpoint="endpoint_0"} 3.395
```

The example config in [`config/`](config) covers Z-Wave JS, Zigbee2MQTT and a Mosquitto broker's
own `$SYS` telemetry: 28 rules producing 32 metric families.

## Setting it up on unraid

You need the **Docker Compose Manager** plugin from Community Applications, a reachable MQTT
broker, and somewhere to scrape from. The client speaks MQTT 5, so Mosquitto 1.6 or newer.

### 1. Put the config on your array

```
/mnt/user/appdata/mqtt2prometheus/
├── mqtt2prometheus.yaml
└── sources/
    ├── zwave.yaml
    ├── zigbee2mqtt.yaml
    └── mosquitto.yaml
```

Copy the files from [`config/`](config) as a starting point. Delete the sources you do not have.

### 2. Add the stack

In the Docker Compose Manager plugin, add a stack and paste:

```yaml
services:
  mqtt2prometheus:
    image: ghcr.io/slakje-nl/mqtt2prometheus:latest
    container_name: mqtt2prometheus
    restart: unless-stopped
    ports:
      - '9000:9000'
    volumes:
      - /mnt/user/appdata/mqtt2prometheus:/config:ro
    environment:
      MQTT_BROKER: ${MQTT_BROKER:?e.g. tcp://192.0.2.10:1883}
      MQTT_USERNAME: ${MQTT_USERNAME:?}
      MQTT_PASSWORD: ${MQTT_PASSWORD:?}
```

Put the three values in the stack's `.env`. The broker address and the credentials are injected at
runtime and never live in the config files, so the config directory is safe to copy around and to
paste into an issue.

### 3. Check what it is allowed to reach

Worth doing for any container you did not build yourself. This one asks for:

- **No host networking**, no `network_mode: host`.
- **No privileged mode**, no added capabilities.
- **No Docker socket.** It never talks to Docker.
- **One bind mount, read only**: your config directory at `/config`.
- **One published port**: 9000, for Prometheus to scrape.
- It runs as **nonroot** on a distroless base, about 13 MB, with no shell in the image.

If the compose file above ever asks for more than that, something is wrong.

### 4. Confirm it works

```
curl http://<unraid-ip>:9000/metrics
```

You should see your devices within a scrape or two. There is also `/healthz` (the process is up)
and `/readyz` (connected to the broker and at least one message seen).

To check a config change before restarting anything:

```
docker exec mqtt2prometheus mqtt2prometheus verify
```

It prints what every source resolved to and exits non-zero on any problem. Credentials are
redacted in the output.

### 5. Point Prometheus at it

```yaml
scrape_configs:
  - job_name: mqtt2prometheus
    static_configs:
      - targets: ['<unraid-ip>:9000']
```

### 6. Add a metric

Say a new plug shows up publishing `zigbee2mqtt/desk lamp` with `{"power":8.2,"battery":97}`.
`battery` is not in the example config, so add one rule to `sources/zigbee2mqtt.yaml`:

```yaml
  - match: '^zigbee2mqtt/(?P<device>[^/]+)$'
    metric_name: zigbee_battery_percent
    type: gauge
    help: Battery charge in percent
    value: {from: json, path: battery}
```

Verify it, then restart the container. Devices without a `battery` field are unaffected: a rule
that finds nothing produces nothing rather than discarding the whole message.

### Upgrading and rolling back

Every merge to `main` publishes `ghcr.io/slakje-nl/mqtt2prometheus:vN` and moves `:latest`. Pin
`:vN` in the compose file if you want upgrades to be a deliberate act, and to have something exact
to roll back to.

## Commands

The image runs `run` by default. Every other subcommand is there to be reached with `docker exec`,
and none of them take a config path — the configuration directory comes from the
`MQTT2PROMETHEUS_CONFIG_DIR` environment variable, which the image already sets to `/config`.

There is no `--log-level` flag either. The log level is `log.level` in the config, and the example
config reads it from `MQTT2PROMETHEUS_LOG_LEVEL`, so one variable sets it for every subcommand.
Logs are JSON on stderr. Leaving the variable unset stops the process naming it, the same way an
unset `MQTT_BROKER` does.

| level | what you get |
|---|---|
| `error` | the broker refused a subscription, or the subscribe call failed |
| `warn` | anything not working while it keeps going: connection attempts failing, a message dropped because a source fell behind |
| `info` | one line per message — topic, source, payload, and whether a rule matched |
| `debug` | why a rule that matched produced no value |

`warn` is the setting to run with. **`info` logs every payload**, which is how you find out why a
rule is not firing — and also means your device readings sit in `docker logs` until the container
is replaced. Turn it on to debug, turn it back off.

```
mqtt2prometheus run                       subscribe and serve /metrics
mqtt2prometheus discover [PREFIX]         print the topic prefixes being published
mqtt2prometheus verify                    check the configuration and exit
mqtt2prometheus healthcheck               probe the local health endpoint and exit
mqtt2prometheus version                   print the version and exit
```

```
docker exec mqtt2prometheus mqtt2prometheus verify
```

### Finding out what is publishing

Before you can write a rule you need to know the topic. `discover` prints each prefix once, the
moment it first appears, and nothing else:

```
$ docker exec mqtt2prometheus mqtt2prometheus discover
dsmr/reading
zigbee2mqtt
$SYS/broker
```

Only prefixes go to stdout, so `discover > topics.txt` gives you a clean list. Progress goes to
stderr: `waiting for messages` when it starts, `closing` when it stops, and nothing in between
unless the broker misbehaves.

Pass a prefix to narrow it — `discover dsmr` subscribes to `dsmr/#` — and `--depth` to change how
many segments make a prefix (two by default). `--for` takes a duration (`--for 5m`) and `--count`
stops after that many prefixes; `--for 0` and `--count 0` mean no limit. Ctrl-C always stops
cleanly.

Retained messages arrive the moment it subscribes, so a device that only publishes hourly still
shows up in the first few seconds. It connects with its own client id, so running it never
disturbs the exporter already connected to the same broker.

## Configuration

### Process

`config/mqtt2prometheus.yaml`. Every field is required; there are no defaults, so a value you
forgot fails loudly at startup instead of silently becoming something the file does not document.

| Field | Meaning |
|---|---|
| `mqtt.broker` | Broker URL, for example `tcp://192.0.2.10:1883` |
| `mqtt.client_id` | Client id to connect with |
| `mqtt.username`, `mqtt.password` | Credentials |
| `mqtt.qos` | Default subscription QoS, 0, 1 or 2 |
| `mqtt.clean_session` | Whether to start a fresh session |
| `server.listen` | Address to serve `/metrics` on |
| `log.level` | `debug`, `info`, `warn` or `error` |
| `sources` | Glob for the source files, relative to the config directory |

`${ENV_VAR}` is expanded anywhere in any config file.

### Sources

One file per source, in `sources/`. Sources pointing at the same broker share one connection.

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Identifies the source in logs and self-metrics |
| `subscribe` | yes | MQTT topic filter to subscribe to |
| `qos` | no | Overrides `mqtt.qos` for this source |
| `broker` | no | Connect to a different broker |
| `last_updated_metric` | no | Gauge set to the current time whenever any rule here matches |
| `labels` | no | Labels added to every metric from this source |
| `rules` | yes | At least one |

### Rules

| Field | Required | Meaning |
|---|---|---|
| `match` | yes | Go regular expression; named groups become labels |
| `metric_name` | yes | The Prometheus metric name |
| `type` | yes | `gauge` or `counter` |
| `help` | no | The `# HELP` text |
| `value` | yes | Where the number comes from |
| `last_updated_metric` | no | Gauge set to the current time, carrying this rule's labels |
| `labels` | no | Extra labels; `{capture}` is expanded, and a capture used here stops emitting a label of its own |

Every rule that matches a topic runs, so one message can produce several metrics.

### Values

```yaml
value: {from: json, path: value}                    # dotted path into the object
value: {from: json, path: value, scale: 0.001}      # multiply after extraction
value: {from: json, path: state, map: {ON: 1, OFF: 0}}
value: {from: raw}                                  # the whole payload as a number
value: {from: raw, regex: '^([0-9]+) seconds$'}     # group 1 as the number
```

A path that is not present, a null, and a string outside `map` are all skipped quietly. Malformed
JSON, a non-numeric value and a `regex` that does not match are counted as errors.

A `counter` whose value drops is treated as a source restart: an offset is carried so `rate()` and
`increase()` stay correct.

## Metrics about the exporter itself

```
mqtt2prom_build_info{version,commit,go_version}
mqtt2prom_mqtt_connected
mqtt2prom_mqtt_reconnects_total
mqtt2prom_messages_received_total{source}
mqtt2prom_messages_dropped_total
mqtt2prom_message_errors_total{source,reason}
mqtt2prom_series
```

Every label here is bounded by your config, so none of them can grow without limit.

## Working on it

```
just            list the recipes
just check      everything below: format, test and security
just format     gofmt, vet, golangci-lint
just test       unit tests with a 100% coverage gate, then the feature tests
just verify     verify config/ without connecting to a broker
just security   govulncheck, gosec, gitleaks
```

The feature tests start a real Mosquitto in a container, so they need Docker.

## Licence

MIT. See [LICENSE](LICENSE).
