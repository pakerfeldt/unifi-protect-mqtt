package mqtt

import (
	"encoding/json"
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog"

	"github.com/pa/unifi-protect-mqtt/internal/protect"
)

// Publisher publishes smart detection events to MQTT.
type Publisher struct {
	client      paho.Client
	topicPrefix string
	logger      *zerolog.Logger
}

// NewPublisher creates a new MQTT publisher and connects to the broker.
func NewPublisher(broker, username, password, topicPrefix string, logger *zerolog.Logger) (*Publisher, error) {
	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("unifi-protect-mqtt-%d", time.Now().UnixNano())).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetOnConnectHandler(func(_ paho.Client) {
			logger.Info().Str("broker", broker).Msg("connected to MQTT broker")
		}).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			logger.Warn().Err(err).Msg("MQTT connection lost")
		})

	if username != "" {
		opts.SetUsername(username)
	}
	if password != "" {
		opts.SetPassword(password)
	}

	client := paho.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connecting to MQTT broker: %w", err)
	}

	return &Publisher{
		client:      client,
		topicPrefix: topicPrefix,
		logger:      logger,
	}, nil
}

// PublishEvent publishes a smart detection event to the appropriate MQTT topic.
//
// Topic structure:
//
//	{prefix}/events/{type}/{camera}  - e.g. unifi/protect/events/person/front_door
//
// Subscribers can use MQTT wildcards for aggregation:
//
//	{prefix}/events/#                - all events, all cameras
//	{prefix}/events/person/+         - all person events
//	{prefix}/events/+/front_door     - all events from one camera
func (p *Publisher) PublishEvent(event protect.SmartDetectEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	topic := fmt.Sprintf("%s/events/%s/%s", p.topicPrefix, event.Type, sanitizeTopic(event.CameraName))
	token := p.client.Publish(topic, 0, false, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("publishing to %s: %w", topic, err)
	}

	p.logger.Debug().
		Str("type", event.Type).
		Str("camera", event.CameraName).
		Str("topic", topic).
		Msg("published event")

	return nil
}

// Disconnect cleanly shuts down the MQTT connection.
func (p *Publisher) Disconnect() {
	p.client.Disconnect(1000)
}

// sanitizeTopic replaces spaces and special characters in camera names
// to make them safe for MQTT topic segments.
func sanitizeTopic(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.':
			result = append(result, c)
		case c == ' ', c == '_':
			result = append(result, '_')
		default:
			// Skip characters that aren't topic-safe.
		}
	}
	return string(result)
}
