# Commands

The image runs `run` by default. Every other subcommand is there to be reached with `docker exec`,
and none of them take a config path — the configuration directory comes from the
`MQTT2PROMETHEUS_CONFIG_DIR` environment variable, which the image already sets to `/config`.

```
mqtt2prometheus run                       subscribe and serve /metrics
mqtt2prometheus discover [PREFIX]         print the topic prefixes being published
mqtt2prometheus capture FILTER            print each message as topic and payload
mqtt2prometheus verify                    check the configuration and exit
mqtt2prometheus healthcheck               probe the local health endpoint and exit
mqtt2prometheus version                   print the version and exit
```

```
docker exec mqtt2prometheus mqtt2prometheus verify
```

## Log levels

There is no `--log-level` flag. The log level is `log.level` in the config, and the example
config reads it from `MQTT2PROMETHEUS_LOG_LEVEL`, so one variable sets it for every subcommand.
Logs are JSON on stderr. `compose.yaml` defaults the variable to `warn`; running the binary outside
compose with it unset stops the process naming it, the same way an unset `MQTT_BROKER` does.

| level | what you get |
|---|---|
| `error` | the broker refused a subscription, or the subscribe call failed |
| `warn` | anything not working while it keeps going: connection attempts failing, a message dropped because a source fell behind |
| `info` | one line per message — topic, source, payload, and whether a rule matched |
| `debug` | why a rule that matched produced no value |

`warn` is the setting to run with. **`info` logs every payload**, which is how you find out why a
rule is not firing — and also means your device readings sit in `docker logs` until the container
is replaced. Turn it on to debug, turn it back off.

## Checking a config change

To check a config change before restarting anything:

```
docker exec mqtt2prometheus mqtt2prometheus verify
```

It prints what every source resolved to and exits non-zero on any problem. Credentials are
redacted in the output.

There is no reload signal: once `verify` is happy, restart the container to pick the change up.

Besides `/metrics`, the server also serves `/healthz` (the process is up) and `/readyz`
(connected to the broker and at least one message seen).

## Finding out what is publishing

Before you can write a rule you need to know the topic. `discover` prints each prefix once, the
moment it first appears, and nothing else:

```
$ docker exec mqtt2prometheus mqtt2prometheus discover
dsmr
zigbee2mqtt
$SYS
```

Only prefixes go to stdout, so `discover > topics.txt` gives you a clean list. Progress goes to
stderr: `waiting for messages` when it starts, `closing` when it stops, and nothing in between
unless the broker misbehaves.

Pass a prefix to narrow it — `discover dsmr` subscribes to `dsmr/#` — and `-depth` to change how
many segments make a prefix; one by default, so `-depth 2` turns `dsmr` into `dsmr/reading`. `-for`
takes a duration (`-for 5m`) and `-count` stops after that many prefixes; `-for 0` and `-count 0`
mean no limit. Ctrl-C always stops cleanly.

Retained messages arrive the moment it subscribes, so a device that only publishes hourly still
shows up in the first few seconds. It connects with its own client id, so running it never
disturbs the exporter already connected to the same broker.

## Seeing what a topic actually sends

Once `discover` has told you the prefix, `capture` prints the messages themselves — one line per
message, topic and payload separated by a tab:

```
$ docker exec mqtt2prometheus mqtt2prometheus capture 'dsmr/#'
dsmr/reading/electricity_currently_delivered	0.412
dsmr/reading/gas_meter_reading	1234.567
dsmr/consumption	{"power":413,"tariff":"0002","timestamp":"250824120000W"}
```

**Everything it prints was published while you were watching.** It asks the broker to withhold
retained messages, so the output is live traffic rather than a snapshot of values that may be
hours old — which also means `-count 5` is five real messages. The trade is that a device which
only publishes on change may say nothing for a long time; `discover` still lists it, because
`discover` deliberately keeps retained messages in order to map the tree.

One line is always one message: a newline, carriage return or tab inside a payload is written as
`\n`, `\r` or `\t`. Backslashes are left alone so JSON stays readable. That makes the output
easy to slice:

```
mqtt2prometheus capture 'dsmr/#' | cut -f1 | sort -u      # just the topics
mqtt2prometheus capture 'dsmr/#' | cut -f2                # just the payloads
```

`-for` and `-count` bound the run exactly as they do for `discover`, counting messages rather
than prefixes.
