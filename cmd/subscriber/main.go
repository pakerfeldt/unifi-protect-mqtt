// Command subscriber is a minimal MQTT test client that subscribes to
// unifi-protect-mqtt event topics and prints every message it receives,
// including retained messages delivered on connect.
//
// Configuration is read from environment variables, with
// flags available as overrides.
//
// Usage:
//
//	go run ./cmd/subscriber
//	go run ./cmd/subscriber -broker tcp://10.0.0.5:1883 -prefix unifi/protect
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/pa/unifi-protect-mqtt/internal/config"
)

func main() {
	// Flags override env values.
	broker := flag.String("broker", config.GetEnv("MQTT_BROKER", "tcp://localhost:1883"), "MQTT broker URL")
	prefix := flag.String("prefix", config.GetEnv("MQTT_TOPIC_PREFIX", "unifi/protect"), "MQTT topic prefix")
	username := flag.String("username", config.GetEnv("MQTT_USERNAME", ""), "MQTT username")
	password := flag.String("password", config.GetEnv("MQTT_PASSWORD", ""), "MQTT password")
	flag.Parse()

	topic := fmt.Sprintf("%s/events/#", *prefix)

	opts := paho.NewClientOptions().
		AddBroker(*broker).
		SetClientID(fmt.Sprintf("protect-sub-%d", time.Now().UnixMilli())).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	if *username != "" {
		opts.SetUsername(*username)
	}
	if *password != "" {
		opts.SetPassword(*password)
	}

	opts.SetOnConnectHandler(func(c paho.Client) {
		log.Printf("connected to %s, subscribing to %s", *broker, topic)
		token := c.Subscribe(topic, 1, nil)
		token.Wait()
		if err := token.Error(); err != nil {
			log.Fatalf("subscribe failed: %v", err)
		}
	})

	opts.SetDefaultPublishHandler(func(_ paho.Client, msg paho.Message) {
		retained := ""
		if msg.Retained() {
			retained = " [retained]"
		}

		// Pretty-print JSON if possible, otherwise print raw.
		var pretty json.RawMessage
		if err := json.Unmarshal(msg.Payload(), &pretty); err == nil {
			formatted, _ := json.MarshalIndent(pretty, "  ", "  ")
			fmt.Printf("\n--- %s%s ---\n  %s\n", msg.Topic(), retained, formatted)
		} else {
			fmt.Printf("\n--- %s%s ---\n  %s\n", msg.Topic(), retained, msg.Payload())
		}
	})

	client := paho.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		log.Fatalf("connect failed: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	log.Println("disconnecting...")
	client.Disconnect(1000)
}
