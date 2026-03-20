package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/pa/unifi-protect-mqtt/internal/config"
	"github.com/pa/unifi-protect-mqtt/internal/mqtt"
	"github.com/pa/unifi-protect-mqtt/internal/protect"
	"github.com/pa/unifi-protect-mqtt/internal/proxy"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger().
		Level(zerolog.DebugLevel)

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load config")
	}

	logger.Info().
		Str("unifi_host", cfg.UnifiHost).
		Str("mqtt_broker", cfg.MQTTBroker).
		Str("topic_prefix", cfg.MQTTTopicPrefix).
		Msg("starting unifi-protect-mqtt")

	// Set up context with signal handling for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info().Str("signal", sig.String()).Msg("received signal, shutting down")
		cancel()
	}()

	// Connect to MQTT broker.
	pub, err := mqtt.NewPublisher(
		cfg.MQTTBroker,
		cfg.MQTTUsername,
		cfg.MQTTPassword,
		cfg.MQTTTopicPrefix,
		&logger,
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to MQTT broker")
	}
	defer pub.Disconnect()

	// Create UniFi Protect client and authenticate.
	client := protect.NewClient(cfg.UnifiHost, cfg.UnifiUsername, cfg.UnifiPassword, cfg.UnifiExternalHost, cfg.ProxyBaseURL, &logger)
	if err := client.Authenticate(ctx); err != nil {
		logger.Fatal().Err(err).Msg("failed to authenticate with UniFi Protect")
	}

	// Start the proxy server for auth-free thumbnail/video access.
	proxySrv := proxy.NewServer(cfg.ProxyListenAddr, client, &logger)
	go func() {
		if err := proxySrv.ListenAndServe(); err != nil {
			logger.Fatal().Err(err).Msg("proxy server failed")
		}
	}()
	defer proxySrv.Shutdown(context.Background())

	// Event channel bridges the websocket stream to the MQTT publisher.
	events := make(chan protect.SmartDetectEvent, 64)

	// Start the MQTT publishing goroutine.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if err := pub.PublishEvent(event); err != nil {
					logger.Error().Err(err).Msg("failed to publish event")
				}
			}
		}
	}()

	// Replay recent events from the Protect API if configured.
	if cfg.ReplayLastEvents > 0 {
		logger.Info().Int("count", cfg.ReplayLastEvents).Msg("replaying recent events")
		recent, err := client.GetRecentEvents(ctx, cfg.ReplayLastEvents)
		if err != nil {
			logger.Error().Err(err).Msg("failed to fetch recent events for replay")
		} else {
			for _, event := range recent {
				if err := pub.PublishEvent(event); err != nil {
					logger.Error().Err(err).Msg("failed to publish replayed event")
				}
			}
			logger.Info().Int("published", len(recent)).Msg("replayed recent events")
		}
	}

	// Stream events from UniFi Protect (blocks until context is cancelled,
	// handles reconnection internally).
	logger.Info().Msg("listening for smart detection events...")
	client.StreamEvents(ctx, events)

	logger.Info().Msg("shutdown complete")
}
