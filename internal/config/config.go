package config

type Config struct {
	MQTT    MQTT   `yaml:"mqtt"`
	Server  Server `yaml:"server"`
	Log     Log    `yaml:"log"`
	Sources string `yaml:"sources"`

	SourceList []Source `yaml:"-"`
}

type MQTT struct {
	Broker       string `yaml:"broker"`
	ClientID     string `yaml:"client_id"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	QoS          *uint8 `yaml:"qos"`
	CleanSession *bool  `yaml:"clean_session"`
}

type Server struct {
	Listen string `yaml:"listen"`
}

type Log struct {
	Level string `yaml:"level"`
}

type Source struct {
	Name              string           `yaml:"name"`
	Broker            string           `yaml:"broker"`
	Subscribe         string           `yaml:"subscribe"`
	QoS               *uint8           `yaml:"qos"`
	LastUpdatedMetric string           `yaml:"last_updated_metric"`
	Labels            map[string]Label `yaml:"labels"`
	Rules             []Rule           `yaml:"rules"`

	Path string `yaml:"-"`
}

type Rule struct {
	Match             string           `yaml:"match"`
	MetricName        string           `yaml:"metric_name"`
	Type              string           `yaml:"type"`
	Help              string           `yaml:"help"`
	Value             Value            `yaml:"value"`
	LastUpdatedMetric string           `yaml:"last_updated_metric"`
	Labels            map[string]Label `yaml:"labels"`
}

type Label struct {
	From  string            `yaml:"from"`
	Value string            `yaml:"value"`
	Path  string            `yaml:"path"`
	Map   map[string]string `yaml:"map"`
}

type Value struct {
	From  string             `yaml:"from"`
	Path  string             `yaml:"path"`
	Regex string             `yaml:"regex"`
	Scale *float64           `yaml:"scale"`
	Map   map[string]float64 `yaml:"map"`
}

const HeartbeatHelp = "Unix time of the last message this rule matched"

const (
	TypeGauge   = "gauge"
	TypeCounter = "counter"

	FromJSON   = "json"
	FromRaw    = "raw"
	FromStatic = "static"
)

func (s Source) EffectiveQoS(global uint8) uint8 {
	if s.QoS != nil {
		return *s.QoS
	}

	return global
}

func (s Source) EffectiveBroker(global string) string {
	if s.Broker != "" {
		return s.Broker
	}

	return global
}
