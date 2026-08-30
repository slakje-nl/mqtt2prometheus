# mqtt2prometheus

Subscribes to MQTT topics and exposes selected messages as Prometheus metrics. The mapping from
topic to metric lives entirely in config, so adding a sensor is a config edit and a restart rather
than a code change and a rebuild.

> [!WARNING]
> **Always verify code you use from the internet.** That applies to this repository as much as
> any other. Read the source, understand what it can reach, and decide for yourself before you
> run it on infrastructure you care about.

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

You need a way to run a compose stack on the box —
[compose2unraid](https://github.com/slakje-nl/compose2unraid) or the **Docker Compose Manager**
plugin from Community Applications both work — a reachable MQTT broker, and somewhere to scrape
from. The client speaks MQTT 5, so Mosquitto 1.6 or newer.

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

Copy [`compose.yaml`](compose.yaml) and [`.env.example`](.env.example) from this repository into a
stack — a directory under compose2unraid's `stacks/`, or a new stack in Docker Compose Manager —
and rename `.env.example` to `.env` next to it:

```
MQTT_BROKER=tcp://192.0.2.10:1883
MQTT_USERNAME=
MQTT_PASSWORD=
#MQTT2PROMETHEUS_LOG_LEVEL=info
```

The broker address and the credentials are injected at runtime and never live in the config files,
so the config directory is safe to copy around and to paste into an issue. `.env` is the one file
that holds them, and it is the one file you never commit anywhere. Any of the three left unset stops
the stack with a message naming it rather than starting something that connects nowhere. The log
level is the exception: `compose.yaml` defaults it to `warn`, so you only set it to turn debugging
on.

If your appdata lives somewhere other than `/mnt/user/appdata/mqtt2prometheus`, change the bind
mount in `compose.yaml`; the `/config` side of it is what the image expects and should stay.

### 3. Confirm it works

```
curl http://<unraid-ip>:9000/metrics
```

You should see your devices within a scrape or two.

### 4. Point Prometheus at it

```yaml
scrape_configs:
  - job_name: mqtt2prometheus
    static_configs:
      - targets: ['<unraid-ip>:9000']
```

### 5. Add a metric

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
for example `docker exec mqtt2prometheus mqtt2prometheus verify`:

```
mqtt2prometheus run                       subscribe and serve /metrics
mqtt2prometheus discover [PREFIX]         print the topic prefixes being published
mqtt2prometheus capture FILTER            print each message as topic and payload
mqtt2prometheus verify                    check the configuration and exit
mqtt2prometheus healthcheck               probe the local health endpoint and exit
mqtt2prometheus version                   print the version and exit
```

## Documentation

- [Configuration](docs/configuration.md) — the process config, sources, rules, labels, values,
  and the exporter's own metrics.

## Working on it

```
just            list the recipes
just check      everything below: format, test and security
just format     gofmt, vet, golangci-lint
just test       unit tests with a 100% coverage gate, then the feature tests
just verify     verify config/ without connecting to a broker
just security   govulncheck, gosec, gitleaks
just compose-check  parse compose.yaml the way CI does
```

The feature tests start a real Mosquitto in a container, so they need Docker.

## Licence

MIT. See [LICENSE](LICENSE).

## AI

Claude was used responsibly in the development of this repository. It was not left on its own:
it was monitored and guided by a human (who is also a software engineer!).
