package main

import (
	"fmt"
	"log"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/pa/unifi-protect-mqtt/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning loading config: %v. Using loaded MQTT defaults.", err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	broker := cfg.MQTTBroker
	if broker == "" {
		broker = "tcp://localhost:1883"
	}
	topicPrefix := cfg.MQTTTopicPrefix
	if topicPrefix == "" {
		topicPrefix = "unifi/protect"
	}

	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("unifi-protect-mqtt-clear-%d", time.Now().UnixNano()))

	if cfg.MQTTUsername != "" {
		opts.SetUsername(cfg.MQTTUsername)
	}
	if cfg.MQTTPassword != "" {
		opts.SetPassword(cfg.MQTTPassword)
	}

	client := paho.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("Error connecting to MQTT broker: %v", token.Error())
	}

	topicPattern := fmt.Sprintf("%s/events/#", topicPrefix)
	fmt.Printf("Subscribing to %s to clear retained messages...\n", topicPattern)

	token := client.Subscribe(topicPattern, 0, func(client paho.Client, msg paho.Message) {
		topic := msg.Topic()

		if msg.Retained() && len(msg.Payload()) > 0 {
			fmt.Printf("Clearing retained message on topic: %s\n", topic)
			client.Publish(topic, 1, true, []byte{})
		}
	})

	token.Wait()
	if err := token.Error(); err != nil {
		log.Fatalf("Error subscribing: %v", err)
	}

	fmt.Println("Listening for retained messages for 3 seconds...")
	time.Sleep(3 * time.Second)

	client.Disconnect(250)
	fmt.Println("Done clearing retained messages.")
}
