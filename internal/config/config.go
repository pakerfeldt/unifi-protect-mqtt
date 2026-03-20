package config

import (
	"bufio"
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
	env, err := LoadEnvFile(".env")
	if err != nil {
		return nil, fmt.Errorf("failed to load .env file: %w", err)
	}

	cfg := &Config{
		UnifiHost:         GetEnv(env, "UNIFI_HOST", ""),
		UnifiUsername:     GetEnv(env, "UNIFI_USERNAME", ""),
		UnifiPassword:     GetEnv(env, "UNIFI_PASSWORD", ""),
		UnifiExternalHost: strings.TrimRight(GetEnv(env, "UNIFI_EXTERNAL_HOST", ""), "/"),
		MQTTBroker:        GetEnv(env, "MQTT_BROKER", "tcp://localhost:1883"),
		MQTTUsername:      GetEnv(env, "MQTT_USERNAME", ""),
		MQTTPassword:      GetEnv(env, "MQTT_PASSWORD", ""),
		MQTTTopicPrefix:   GetEnv(env, "MQTT_TOPIC_PREFIX", "unifi/protect"),
		ProxyListenAddr:   GetEnv(env, "PROXY_LISTEN_ADDR", ":8080"),
		ProxyBaseURL:      strings.TrimRight(GetEnv(env, "PROXY_BASE_URL", "http://localhost:8080"), "/"),
		ReplayLastEvents:  getEnvInt(env, "REPLAY_LAST_EVENTS", 0),
	}

	if cfg.UnifiHost == "" {
		return nil, fmt.Errorf("UNIFI_HOST is required")
	}
	if cfg.UnifiUsername == "" {
		return nil, fmt.Errorf("UNIFI_USERNAME is required")
	}
	if cfg.UnifiPassword == "" {
		return nil, fmt.Errorf("UNIFI_PASSWORD is required")
	}

	return cfg, nil
}

// LoadEnvFile parses a .env file into a key-value map.
// Lines that are empty or start with '#' are skipped.
func LoadEnvFile(path string) (map[string]string, error) {
	env := make(map[string]string)

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes if present.
		value = strings.Trim(value, `"'`)
		env[key] = value
	}

	return env, scanner.Err()
}

// GetEnv returns the value from the env map, falling back to os env, then default.
func GetEnv(env map[string]string, key, fallback string) string {
	if v, ok := env[key]; ok && v != "" {
		return v
	}
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt returns an integer value from the env map, falling back to os env, then default.
func getEnvInt(env map[string]string, key string, fallback int) int {
	raw := GetEnv(env, key, "")
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}
