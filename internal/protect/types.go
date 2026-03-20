package protect

import "time"

// Bootstrap represents the UniFi Protect bootstrap response.
// We only include the fields we need.
type Bootstrap struct {
	AuthUserID   string            `json:"authUserId"`
	AccessKey    string            `json:"accessKey"`
	Cameras      []BootstrapCamera `json:"cameras"`
	LastUpdateID string            `json:"lastUpdateId"`
}

// BootstrapCamera holds the camera info we need from bootstrap.
type BootstrapCamera struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	State    string `json:"state"`
	ModelKey string `json:"modelKey"`
	Mac      string `json:"mac"`
	Host     string `json:"host"`
}

// EventPayload represents the payload of a websocket event update.
// This is a partial representation - we only decode what we need.
type EventPayload struct {
	ID                string   `json:"id"`
	Type              string   `json:"type"`
	ModelKey          string   `json:"modelKey"`
	Camera            string   `json:"camera"`
	SmartDetectTypes  []string `json:"smartDetectTypes"`
	SmartDetectEvents []string `json:"smartDetectEvents"`
	Score             int      `json:"score"`
	Start             int64    `json:"start"`
	End               int64    `json:"end"`
}

// SmartDetectEvent is emitted when a person or animal is detected.
type SmartDetectEvent struct {
	EventID      string    `json:"event_id"`
	Type         string    `json:"type"` // "person" or "animal"
	Camera       string    `json:"camera_id"`
	CameraName   string    `json:"camera_name"`
	Score        int       `json:"score"`
	Start        int64     `json:"start"`
	End          int64     `json:"end"`
	Timestamp    time.Time `json:"timestamp"`
	ThumbnailURL string    `json:"thumbnail_url"`
	VideoURL     string    `json:"video_url"`
	PlaybackURL  string    `json:"playback_url"`
	Early        bool      `json:"-"` // routing flag, not serialized
}

// APIEvent represents an event returned by the UniFi Protect REST API
// (GET /proxy/protect/api/events). Only fields we need are included.
type APIEvent struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Camera           string   `json:"camera"`
	SmartDetectTypes []string `json:"smartDetectTypes"`
	Score            int      `json:"score"`
	Start            int64    `json:"start"`
	End              int64    `json:"end"`
}
