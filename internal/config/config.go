package config

import (
	"fmt"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	// UniFi Protect settings
	UnifiHost         string `envconfig:"UNIFI_HOST"`
	UnifiUsername     string `envconfig:"UNIFI_USERNAME"`
	UnifiPassword     string `envconfig:"UNIFI_PASSWORD"`
	UnifiExternalHost string `envconfig:"UNIFI_EXTERNAL_HOST"` // Optional: e.g. "https://unifi.ui.com/consoles/XXXX:unifi-protect"

	// MQTT settings
	MQTTBroker      string `envconfig:"MQTT_BROKER" default:"tcp://localhost:1883"`
	MQTTUsername    string `envconfig:"MQTT_USERNAME"`
	MQTTPassword    string `envconfig:"MQTT_PASSWORD"`
	MQTTTopicPrefix string `envconfig:"MQTT_TOPIC_PREFIX" default:"unifi/protect"`

	// Proxy server settings
	ProxyListenAddr string `envconfig:"PROXY_LISTEN_ADDR" default:":8080"`
	ProxyBaseURL    string `envconfig:"PROXY_BASE_URL" default:"http://localhost:8080"`

	// Replay settings
	ReplayLastEvents int `envconfig:"REPLAY_LAST_EVENTS" default:"0"` // Number of recent events to replay on startup (0 = disabled)
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config

	if err := envconfig.Process("", &cfg); err != nil {
		return &cfg, fmt.Errorf("parsing environment variables: %w", err)
	}

	// Normalize optional strings
	cfg.UnifiExternalHost = strings.TrimRight(cfg.UnifiExternalHost, "/")
	cfg.ProxyBaseURL = strings.TrimRight(cfg.ProxyBaseURL, "/")

	// Ensure MQTTBroker has a scheme
	if cfg.MQTTBroker != "" && !strings.Contains(cfg.MQTTBroker, "://") {
		cfg.MQTTBroker = "tcp://" + cfg.MQTTBroker
	}

	// Validate required fields
	if cfg.UnifiHost == "" {
		return &cfg, fmt.Errorf("UNIFI_HOST is required")
	}
	if cfg.UnifiUsername == "" {
		return &cfg, fmt.Errorf("UNIFI_USERNAME is required")
	}
	if cfg.UnifiPassword == "" {
		return &cfg, fmt.Errorf("UNIFI_PASSWORD is required")
	}

	return &cfg, nil
}
