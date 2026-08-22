package app

import (
	"fmt"
	"io"
	"strings"
)

func Verify(dir string, out io.Writer) error {
	a := &App{dir: dir}

	cfg, sources, err := a.load()
	if err != nil {
		return err
	}

	lines := []string{
		fmt.Sprintf("broker      %s", redactURL(cfg.MQTT.Broker)),
		fmt.Sprintf("client id   %s", cfg.MQTT.ClientID),
		fmt.Sprintf("username    %s", redact(cfg.MQTT.Username)),
		fmt.Sprintf("password    %s", redact(cfg.MQTT.Password)),
		fmt.Sprintf("listen      %s", cfg.Server.Listen),
		"",
	}

	metrics := map[string]struct{}{}

	for _, src := range sources {
		lines = append(lines, fmt.Sprintf("%-14s %2d rules  subscribes %-18s qos %d",
			src.name, len(src.rules), src.subscribe, src.qos))

		for _, name := range src.metricNames() {
			metrics[name] = struct{}{}
		}
	}

	lines = append(lines, "", fmt.Sprintf("%d sources, %d rules, %d metrics, no problems",
		len(sources), countRules(sources), len(metrics)))

	if _, err := io.WriteString(out, strings.Join(lines, "\n")+"\n"); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}

	return nil
}

func countRules(sources []*source) int {
	total := 0
	for _, src := range sources {
		total += len(src.rules)
	}

	return total
}

func redact(value string) string {
	if value == "" {
		return "(empty)"
	}

	return "(set, redacted)"
}

func redactURL(raw string) string {
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return raw
	}

	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return "(redacted)" + raw[at:]
	}

	return raw[:scheme+3] + "(redacted)" + raw[at:]
}
