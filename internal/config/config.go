package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// UniFi Protect settings
	UnifiHost         string
	UnifiUsername     string
	UnifiPassword     string
	UnifiExternalHost string // Optional: e.g. "https://unifi.ui.com/consoles/XXXX:unifi-protect"

	// MQTT settings
	MQTTBroker      string
	MQTTUsername    string
	MQTTPassword    string
	MQTTTopicPrefix string

	// Proxy server settings
	ProxyListenAddr string
	ProxyBaseURL    string

	// Replay settings
	ReplayLastEvents int // Number of recent events to replay on startup (0 = disabled)
}

func Load() (*Config, error) {
	loadEnvFile(".env")

	broker := GetEnv("MQTT_BROKER", "tcp://localhost:1883")
	if !strings.Contains(broker, "://") {
		broker = "tcp://" + broker
	}

	cfg := &Config{
		UnifiHost:         GetEnv("UNIFI_HOST", ""),
		UnifiUsername:     GetEnv("UNIFI_USERNAME", ""),
		UnifiPassword:     GetEnv("UNIFI_PASSWORD", ""),
		UnifiExternalHost: strings.TrimRight(GetEnv("UNIFI_EXTERNAL_HOST", ""), "/"),
		MQTTBroker:        broker,
		MQTTUsername:      GetEnv("MQTT_USERNAME", ""),
		MQTTPassword:      GetEnv("MQTT_PASSWORD", ""),
		MQTTTopicPrefix:   GetEnv("MQTT_TOPIC_PREFIX", "unifi/protect"),
		ProxyListenAddr:   GetEnv("PROXY_LISTEN_ADDR", ":8080"),
		ProxyBaseURL:      strings.TrimRight(GetEnv("PROXY_BASE_URL", "http://localhost:8080"), "/"),
		ReplayLastEvents:  GetEnvInt("REPLAY_LAST_EVENTS", 0),
	}

	if cfg.UnifiHost == "" {
		return cfg, fmt.Errorf("UNIFI_HOST is required")
	}
	if cfg.UnifiUsername == "" {
		return cfg, fmt.Errorf("UNIFI_USERNAME is required")
	}
	if cfg.UnifiPassword == "" {
		return cfg, fmt.Errorf("UNIFI_PASSWORD is required")
	}

	return cfg, nil
}

// GetEnv returns the value from the os env, falling back to a default.
func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetEnvInt returns an integer value from the os env, falling back to a default.
func GetEnvInt(key string, fallback int) int {
	raw := GetEnv(key, "")
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

// loadEnvFile reads a simple .env file and sets the variables into the OS environment
// if they are not already set (OS env vars take priority over .env).
func loadEnvFile(filename string) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return // Ignore missing .env file
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Remove surrounding quotes if any
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[0] == val[len(val)-1] {
			val = val[1 : len(val)-1]
		}

		os.Setenv(key, val)
	}
}
