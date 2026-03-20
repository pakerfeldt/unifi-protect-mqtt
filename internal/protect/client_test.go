package protect

import (
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestMergePayload(t *testing.T) {
	t.Run("merges non-zero fields into empty dst", func(t *testing.T) {
		dst := &EventPayload{}
		src := &EventPayload{
			ID:               "evt-123",
			Camera:           "cam-456",
			SmartDetectTypes: []string{"person"},
			Score:            85,
			Start:            1700000000000,
			End:              1700000005000,
		}
		mergePayload(dst, src)

		if dst.ID != "evt-123" {
			t.Errorf("ID = %q, want %q", dst.ID, "evt-123")
		}
		if dst.Camera != "cam-456" {
			t.Errorf("Camera = %q, want %q", dst.Camera, "cam-456")
		}
		if len(dst.SmartDetectTypes) != 1 || dst.SmartDetectTypes[0] != "person" {
			t.Errorf("SmartDetectTypes = %v, want [person]", dst.SmartDetectTypes)
		}
		if dst.Score != 85 {
			t.Errorf("Score = %d, want 85", dst.Score)
		}
		if dst.Start != 1700000000000 {
			t.Errorf("Start = %d, want 1700000000000", dst.Start)
		}
		if dst.End != 1700000005000 {
			t.Errorf("End = %d, want 1700000005000", dst.End)
		}
	})

	t.Run("does not overwrite with zero values", func(t *testing.T) {
		dst := &EventPayload{
			ID:     "evt-123",
			Camera: "cam-456",
			Score:  85,
			Start:  1700000000000,
		}
		src := &EventPayload{
			// Only SmartDetectTypes is set; everything else is zero.
			SmartDetectTypes: []string{"person"},
		}
		mergePayload(dst, src)

		if dst.ID != "evt-123" {
			t.Errorf("ID was overwritten: got %q", dst.ID)
		}
		if dst.Camera != "cam-456" {
			t.Errorf("Camera was overwritten: got %q", dst.Camera)
		}
		if dst.Score != 85 {
			t.Errorf("Score was overwritten: got %d", dst.Score)
		}
		if dst.Start != 1700000000000 {
			t.Errorf("Start was overwritten: got %d", dst.Start)
		}
		if len(dst.SmartDetectTypes) != 1 || dst.SmartDetectTypes[0] != "person" {
			t.Errorf("SmartDetectTypes not merged: got %v", dst.SmartDetectTypes)
		}
	})

	t.Run("later values overwrite earlier values", func(t *testing.T) {
		dst := &EventPayload{
			Score: 50,
			End:   1700000003000,
		}
		src := &EventPayload{
			Score: 92,
			End:   1700000008000,
		}
		mergePayload(dst, src)

		if dst.Score != 92 {
			t.Errorf("Score = %d, want 92", dst.Score)
		}
		if dst.End != 1700000008000 {
			t.Errorf("End = %d, want 1700000008000", dst.End)
		}
	})
}

// buildWSBinary constructs a raw WebSocket binary message from action and payload JSON.
func buildWSBinary(t *testing.T, action WSAction, payload interface{}) []byte {
	t.Helper()

	actionBytes, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshaling action: %v", err)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling payload: %v", err)
	}

	// Frame 1 (action): header(8) + data
	frame1 := make([]byte, headerSize+len(actionBytes))
	frame1[0] = byte(actionFrame)
	frame1[1] = byte(formatJSON)
	frame1[2] = 0 // not deflated
	frame1[3] = 0 // reserved
	binary.BigEndian.PutUint32(frame1[4:8], uint32(len(actionBytes)))
	copy(frame1[headerSize:], actionBytes)

	// Frame 2 (payload): header(8) + data
	frame2 := make([]byte, headerSize+len(payloadBytes))
	frame2[0] = byte(payloadFrame)
	frame2[1] = byte(formatJSON)
	frame2[2] = 0
	frame2[3] = 0
	binary.BigEndian.PutUint32(frame2[4:8], uint32(len(payloadBytes)))
	copy(frame2[headerSize:], payloadBytes)

	return append(frame1, frame2...)
}

// decodeAndProcess is a test helper that builds, decodes, and processes a WS message.
func decodeAndProcess(t *testing.T, c *Client, action WSAction, payload map[string]interface{}, cameraNames map[string]string, events chan SmartDetectEvent) {
	t.Helper()
	raw := buildWSBinary(t, action, payload)
	decoded, err := DecodeWSMessage(raw)
	if err != nil {
		t.Fatalf("decoding message: %v", err)
	}
	c.processMessage(decoded, cameraNames, events)
}

// drainEvents reads all available events from the channel within a timeout,
// returning them split into early and enriched slices.
func drainEvents(t *testing.T, events chan SmartDetectEvent, timeout time.Duration) (early, enriched []SmartDetectEvent) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-events:
			if ev.Early {
				early = append(early, ev)
			} else {
				enriched = append(enriched, ev)
			}
		case <-deadline:
			return
		}
	}
}

func newTestClient() *Client {
	logger := zerolog.Nop()
	return NewClient("test.local", "user", "pass", "", "http://proxy:8080", &logger)
}

func TestProcessMessage_AccumulatesPartialPayloads(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{"cam-abc": "Front Door"}
	events := make(chan SmartDetectEvent, 64)

	// Message 1: "add" with id, camera, start — but no smartDetectTypes yet.
	decodeAndProcess(t, c, WSAction{Action: "add", ModelKey: "event", ID: "evt-001"},
		map[string]interface{}{
			"id":     "evt-001",
			"camera": "cam-abc",
			"start":  1700000000000,
		}, cameraNames, events)

	// No event should be emitted yet — no smartDetectTypes.
	select {
	case ev := <-events:
		t.Fatalf("unexpected event emitted after msg1: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	// Message 2: "update" with smartDetectTypes (partial patch).
	// This triggers the early event immediately.
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-001"},
		map[string]interface{}{
			"smartDetectTypes": []string{"person"},
		}, cameraNames, events)

	// Early event should arrive immediately with accumulated data so far.
	select {
	case ev := <-events:
		if !ev.Early {
			t.Fatal("expected early event, got enriched")
		}
		if ev.EventID != "evt-001" {
			t.Errorf("early EventID = %q, want %q", ev.EventID, "evt-001")
		}
		if ev.Type != "person" {
			t.Errorf("early Type = %q, want %q", ev.Type, "person")
		}
		if ev.Camera != "cam-abc" {
			t.Errorf("early Camera = %q, want %q", ev.Camera, "cam-abc")
		}
		if ev.CameraName != "Front Door" {
			t.Errorf("early CameraName = %q, want %q", ev.CameraName, "Front Door")
		}
		if ev.Start != 1700000000000 {
			t.Errorf("early Start = %d, want 1700000000000", ev.Start)
		}
		// Score and End are expected to be zero at this point.
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for early event")
	}

	// Message 3: "update" with score and end (arrives within the 2s window).
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-001"},
		map[string]interface{}{
			"score": 87,
			"end":   1700000005000,
		}, cameraNames, events)

	// Wait for the enriched event (2s timer).
	select {
	case ev := <-events:
		if ev.Early {
			t.Fatal("expected enriched event, got early")
		}
		if ev.EventID != "evt-001" {
			t.Errorf("EventID = %q, want %q", ev.EventID, "evt-001")
		}
		if ev.Type != "person" {
			t.Errorf("Type = %q, want %q", ev.Type, "person")
		}
		if ev.Camera != "cam-abc" {
			t.Errorf("Camera = %q, want %q", ev.Camera, "cam-abc")
		}
		if ev.CameraName != "Front Door" {
			t.Errorf("CameraName = %q, want %q", ev.CameraName, "Front Door")
		}
		if ev.Score != 87 {
			t.Errorf("Score = %d, want 87", ev.Score)
		}
		if ev.Start != 1700000000000 {
			t.Errorf("Start = %d, want 1700000000000", ev.Start)
		}
		if ev.End != 1700000005000 {
			t.Errorf("End = %d, want 1700000005000", ev.End)
		}
		if ev.ThumbnailURL != "http://proxy:8080/thumbnail/evt-001?w=640&h=360" {
			t.Errorf("ThumbnailURL = %q", ev.ThumbnailURL)
		}
		if ev.VideoURL != "http://proxy:8080/video/evt-001" {
			t.Errorf("VideoURL = %q", ev.VideoURL)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for enriched event")
	}
}

func TestProcessMessage_EarlyEventEmittedImmediately(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{"cam-abc": "Front Door"}
	events := make(chan SmartDetectEvent, 64)

	// Single message with smartDetectTypes — early event must arrive
	// without waiting for the 2s enrichment delay.
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-fast"},
		map[string]interface{}{
			"smartDetectTypes": []string{"person"},
			"camera":           "cam-abc",
			"score":            65,
			"start":            1700000000000,
		}, cameraNames, events)

	select {
	case ev := <-events:
		if !ev.Early {
			t.Fatal("expected early event")
		}
		if ev.EventID != "evt-fast" {
			t.Errorf("EventID = %q, want %q", ev.EventID, "evt-fast")
		}
		if ev.Score != 65 {
			t.Errorf("Score = %d, want 65", ev.Score)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("early event was not emitted within 500ms")
	}
}

func TestProcessMessage_EarlyEventOnlyOnce(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{"cam-abc": "Front Door"}
	events := make(chan SmartDetectEvent, 64)

	// First message triggers early event.
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-once"},
		map[string]interface{}{
			"smartDetectTypes": []string{"person"},
			"camera":           "cam-abc",
		}, cameraNames, events)

	select {
	case ev := <-events:
		if !ev.Early {
			t.Fatal("expected early event")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for early event")
	}

	// Second message for same event — should NOT emit another early event.
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-once"},
		map[string]interface{}{
			"score": 80,
		}, cameraNames, events)

	// Only the enriched event should arrive after 2s, no second early.
	early, enriched := drainEvents(t, events, 4*time.Second)
	if len(early) != 0 {
		t.Errorf("got %d extra early events, want 0", len(early))
	}
	if len(enriched) != 1 {
		t.Errorf("got %d enriched events, want 1", len(enriched))
	}
}

func TestProcessMessage_UsesActionFrameID(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{"cam-xyz": "Backyard"}
	events := make(chan SmartDetectEvent, 64)

	// Single update message where payload has smartDetectTypes but no "id".
	// The event ID should come from the action frame.
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-999"},
		map[string]interface{}{
			"smartDetectTypes": []string{"animal"},
			"camera":           "cam-xyz",
			"score":            72,
			"start":            1700000010000,
		}, cameraNames, events)

	// Early event arrives immediately.
	select {
	case ev := <-events:
		if ev.EventID != "evt-999" {
			t.Errorf("EventID = %q, want %q", ev.EventID, "evt-999")
		}
		if ev.Type != "animal" {
			t.Errorf("Type = %q, want %q", ev.Type, "animal")
		}
		if ev.CameraName != "Backyard" {
			t.Errorf("CameraName = %q, want %q", ev.CameraName, "Backyard")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event emission")
	}
}

func TestProcessMessage_IgnoresNonEventModelKey(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{}
	events := make(chan SmartDetectEvent, 64)

	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "camera", ID: "cam-001"},
		map[string]interface{}{"name": "Front Door"}, cameraNames, events)

	select {
	case ev := <-events:
		t.Fatalf("unexpected event for non-event modelKey: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestProcessMessage_DeduplicatesWithinCooldown(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{"cam-abc": "Front Door"}
	events := make(chan SmartDetectEvent, 64)

	// First event — emits early + enriched.
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-dup"},
		map[string]interface{}{
			"smartDetectTypes": []string{"person"},
			"camera":           "cam-abc",
			"score":            80,
			"start":            1700000000000,
		}, cameraNames, events)

	// Drain both the early and enriched events.
	early, enriched := drainEvents(t, events, 4*time.Second)
	if len(early) != 1 {
		t.Fatalf("expected 1 early event, got %d", len(early))
	}
	if len(enriched) != 1 {
		t.Fatalf("expected 1 enriched event, got %d", len(enriched))
	}

	// Second event with same ID — should be fully deduplicated (no early, no enriched).
	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-dup"},
		map[string]interface{}{
			"smartDetectTypes": []string{"person"},
			"score":            90,
		}, cameraNames, events)

	early2, enriched2 := drainEvents(t, events, 4*time.Second)
	if len(early2) != 0 {
		t.Errorf("got %d early events on second occurrence, want 0", len(early2))
	}
	if len(enriched2) != 0 {
		t.Errorf("got %d enriched events on second occurrence, want 0", len(enriched2))
	}
}

func TestProcessMessage_FiltersUnsupportedDetectTypes(t *testing.T) {
	c := newTestClient()
	cameraNames := map[string]string{}
	events := make(chan SmartDetectEvent, 64)

	decodeAndProcess(t, c, WSAction{Action: "update", ModelKey: "event", ID: "evt-veh"},
		map[string]interface{}{
			"smartDetectTypes": []string{"vehicle"},
			"camera":           "cam-abc",
		}, cameraNames, events)

	early, enriched := drainEvents(t, events, 3*time.Second)
	if len(early) != 0 {
		t.Errorf("got %d early events for vehicle, want 0", len(early))
	}
	if len(enriched) != 0 {
		t.Errorf("got %d enriched events for vehicle, want 0", len(enriched))
	}
}
