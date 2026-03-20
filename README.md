# unifi-protect-mqtt

Bridges UniFi Protect smart detection events (person, animal, vehicle) to MQTT via WebSocket streaming. Detections are published in real time with metadata and thumbnail URLs.

## How it works

Connects to your UniFi Protect controller, authenticates, and streams the WebSocket event feed. Each smart detection event is published as a JSON payload to an MQTT topic. A built-in HTTP proxy provides auth-free access to thumbnails and video clips.

## Build

```sh
go build ./...
```

Run directly without building:

```sh
go run .
```

## Configuration

Configuration is loaded from environment variables.

```sh
# Example run using environment variables
export UNIFI_HOST=https://192.168.1.1
export UNIFI_USERNAME=your_username
export UNIFI_PASSWORD=your_password
go run .
```

| Variable              | Required | Default                      | Description                                              |
|-----------------------|----------|------------------------------|----------------------------------------------------------|
| `UNIFI_HOST`          | yes      | —                            | Controller address, e.g. `https://192.168.1.1`           |
| `UNIFI_USERNAME`      | yes      | —                            | Local account username                                   |
| `UNIFI_PASSWORD`      | yes      | —                            | Local account password                                   |
| `UNIFI_EXTERNAL_HOST` | no       | `""`                         | UniFi cloud console URL for external access              |
| `MQTT_BROKER`         | no       | `tcp://localhost:1883`       | MQTT broker address                                      |
| `MQTT_USERNAME`       | no       | `""`                         | MQTT username                                            |
| `MQTT_PASSWORD`       | no       | `""`                         | MQTT password                                            |
| `MQTT_TOPIC_PREFIX`   | no       | `unifi/protect`              | Prefix for all published topics                          |
| `PROXY_LISTEN_ADDR`   | no       | `:8080`                      | Address the proxy server listens on                      |
| `PROXY_BASE_URL`      | no       | `http://localhost:8080`      | Public base URL of the proxy (used in published payloads)|
| `REPLAY_LAST_EVENTS`  | no       | `0`                          | Republish the N most recent events on startup            |

## MQTT topics

Events are published to:

```
{MQTT_TOPIC_PREFIX}/events/{type}/{camera}
```

For example: `unifi/protect/events/person/front_door`

MQTT wildcards work as expected:

```
unifi/protect/events/#            # all events, all cameras
unifi/protect/events/person/+    # all person detections
unifi/protect/events/+/front_door # all detections from one camera
```

## License

MIT
