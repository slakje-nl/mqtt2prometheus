# Configuration

## Process

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

## Sources

One file per source, in `sources/`. Sources pointing at the same broker share one connection.

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Identifies the source in logs and self-metrics |
| `subscribe` | yes | MQTT topic filter to subscribe to |
| `qos` | no | Overrides `mqtt.qos` for this source |
| `broker` | no | Connect to a different broker |
| `last_updated_metric` | no | Gauge set to the current time whenever any rule here matches |
| `labels` | no | Labels added to every metric from this source, and to `last_updated_metric` |
| `rules` | yes | At least one |

## Rules

| Field | Required | Meaning |
|---|---|---|
| `match` | yes | Go regular expression; named groups become labels |
| `metric_name` | yes | The Prometheus metric name |
| `type` | yes | `gauge` or `counter` |
| `help` | no | The `# HELP` text |
| `value` | yes | Where the number comes from |
| `last_updated_metric` | no | Gauge set to the current time, carrying this rule's labels |
| `labels` | no | Extra labels, static or taken from the payload |

Every rule that matches a topic runs, so one message can produce several metrics. Several rules may
share a `metric_name` — that is how `phase` or `tariff` becomes a label — as long as they agree on
label names, `help` and `type`. `verify` refuses anything else, including a `last_updated_metric`
that collides with a metric, because Prometheus rejects a scrape whose metric family disagrees with
itself.

## Labels

Every label is a mapping saying where its value comes from. A source's labels land on all of its
rules and on its `last_updated_metric`; a rule's labels are its own, and win on a name clash.

```yaml
labels:
  phase: {from: static, value: l1}
  endpoint: {from: static, value: 'endpoint_{ep}'}   # {ep} is a capture group of match
  mac_address: {from: json, path: mac_address}       # dotted path into the payload
  tariff: {from: json, path: ElectricityTariff, map: {'0001': low, '0002': normal}}
```

`from: json` is how an identifier the device publishes — a gateway's mac address, a meter id —
becomes a label without being written into a config file. A capture group used inside a
`{capture}` template stops emitting a label of its own.

A path that is not present in the payload, and a value outside `map`, skip the whole sample: an
absent label would silently change which series the reading belongs to. A payload that is not JSON,
and a value that is an object or an array, are errors.

## Values

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
