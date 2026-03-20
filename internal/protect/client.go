package protect

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// Client connects to a UniFi Protect controller, authenticates via
// username/password, and streams real-time events over WebSocket.
type Client struct {
	host         string
	username     string
	password     string
	externalHost string // Optional: base URL for external playback links
	proxyBaseURL string // Base URL for proxy-served thumbnail/video links

	httpClient  *http.Client
	proxyClient *http.Client // No timeout, used for streaming proxy responses
	csrfToken   string
	cookies     []*http.Cookie
	mu          sync.Mutex

	// Deduplication: track recently seen event+type pairs so we only
	// emit once per detection (the WS sends add + multiple updates).
	seen   map[string]time.Time
	seenMu sync.Mutex

	// Pending events: accumulate partial payloads across WS messages
	// before emitting. The WS sends partial JSON patches — an "add"
	// followed by multiple "update" messages — so we need to merge
	// fields before building the final SmartDetectEvent.
	pending   map[string]*pendingEvent
	pendingMu sync.Mutex

	logger *zerolog.Logger
}

// pendingEvent accumulates partial event data across WebSocket messages
// before emitting a complete SmartDetectEvent.
type pendingEvent struct {
	payload   EventPayload
	timer     *time.Timer
	created   time.Time
	earlySent bool // true after the immediate early event has been emitted
}

// NewClient creates a new UniFi Protect client.
func NewClient(host, username, password, externalHost, proxyBaseURL string, logger *zerolog.Logger) *Client {
	jar, _ := cookiejar.New(nil)
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // UniFi uses self-signed certs
	}
	return &Client{
		host:         host,
		username:     username,
		password:     password,
		externalHost: externalHost,
		proxyBaseURL: proxyBaseURL,
		logger:       logger,
		seen:         make(map[string]time.Time),
		pending:      make(map[string]*pendingEvent),
		httpClient: &http.Client{
			Jar:       jar,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
			Timeout:   30 * time.Second,
		},
		proxyClient: &http.Client{
			Jar:       jar,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
			// No timeout — responses are streamed to the caller.
		},
	}
}

// Authenticate logs in to the UniFi Protect controller and stores session
// cookies and the CSRF token needed for subsequent requests.
func (c *Client) Authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	baseURL := fmt.Sprintf("https://%s", c.host)

	// First request to get initial CSRF token.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return fmt.Errorf("creating initial request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("initial request: %w", err)
	}
	resp.Body.Close()
	if token := resp.Header.Get("X-CSRF-Token"); token != "" {
		c.csrfToken = token
	}

	// Login request.
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, c.username, c.password)
	loginURL := fmt.Sprintf("%s/api/auth/login", baseURL)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.csrfToken != "" {
		req.Header.Set("X-CSRF-Token", c.csrfToken)
	}

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Store cookies and CSRF token from the login response.
	if token := resp.Header.Get("X-CSRF-Token"); token != "" {
		c.csrfToken = token
	}
	c.cookies = resp.Cookies()

	c.logger.Info().Str("host", c.host).Msg("authenticated with UniFi Protect")
	return nil
}

// GetBootstrap fetches the bootstrap data which includes the lastUpdateId
// needed for the websocket connection, plus camera info.
func (c *Client) GetBootstrap(ctx context.Context) (*Bootstrap, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	url := fmt.Sprintf("https://%s/proxy/protect/api/bootstrap", c.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bootstrap request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized - need to re-authenticate")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bootstrap request failed with status %d", resp.StatusCode)
	}

	if token := resp.Header.Get("X-CSRF-Token"); token != "" {
		c.csrfToken = token
	}

	var bootstrap Bootstrap
	if err := json.NewDecoder(resp.Body).Decode(&bootstrap); err != nil {
		return nil, fmt.Errorf("decoding bootstrap: %w", err)
	}

	c.logger.Info().
		Int("cameras", len(bootstrap.Cameras)).
		Str("lastUpdateId", bootstrap.LastUpdateID).
		Msg("fetched bootstrap")
	return &bootstrap, nil
}

// StreamEvents connects to the WebSocket updates endpoint and sends decoded
// smart detection events to the provided channel. It automatically reconnects
// on disconnection. This function blocks until the context is cancelled.
func (c *Client) StreamEvents(ctx context.Context, events chan<- SmartDetectEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := c.streamOnce(ctx, events); err != nil {
			c.logger.Error().Err(err).Msg("websocket stream error")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			c.logger.Info().Msg("reconnecting websocket...")
			// Re-authenticate before reconnecting.
			if err := c.Authenticate(ctx); err != nil {
				c.logger.Error().Err(err).Msg("re-authentication failed")
			}
		}
	}
}

func (c *Client) streamOnce(ctx context.Context, events chan<- SmartDetectEvent) error {
	// Get bootstrap to know camera names and lastUpdateId.
	bootstrap, err := c.GetBootstrap(ctx)
	if err != nil {
		return fmt.Errorf("getting bootstrap: %w", err)
	}

	// Build camera ID -> name lookup.
	cameraNames := make(map[string]string)
	for _, cam := range bootstrap.Cameras {
		cameraNames[cam.ID] = cam.Name
	}

	c.mu.Lock()
	csrfToken := c.csrfToken
	var cookieStrs []string
	for _, cookie := range c.cookies {
		cookieStrs = append(cookieStrs, cookie.String())
	}
	c.mu.Unlock()

	wsURL := fmt.Sprintf("wss://%s/proxy/protect/ws/updates?lastUpdateId=%s",
		c.host, bootstrap.LastUpdateID)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	headers := http.Header{}
	headers.Set("Cookie", strings.Join(cookieStrs, "; "))
	if csrfToken != "" {
		headers.Set("X-CSRF-Token", csrfToken)
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

	c.logger.Info().Str("url", wsURL).Msg("websocket connected")

	// Keep-alive ping loop.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, rawMessage, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("websocket read: %w", err)
		}

		msg, err := DecodeWSMessage(rawMessage)
		if err != nil {
			// Not all messages can be decoded (some are status updates).
			// Skip silently.
			continue
		}

		c.processMessage(msg, cameraNames, events)
	}
}

func (c *Client) processMessage(msg *WSMessage, cameraNames map[string]string, events chan<- SmartDetectEvent) {
	// We care about "event" model updates that contain smart detections.
	if msg.Action.ModelKey != "event" {
		return
	}

	// The action frame always carries the event ID.
	eventID := msg.Action.ID
	if eventID == "" {
		return
	}

	// Parse the partial payload. WebSocket "update" messages are partial
	// JSON patches — only changed fields are present.
	var payload EventPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	// Prune stale pending entries that never received smartDetectTypes.
	const maxPendingAge = 30 * time.Second
	if len(c.pending) > 100 {
		now := time.Now()
		for k, pe := range c.pending {
			if now.Sub(pe.created) > maxPendingAge {
				if pe.timer != nil {
					pe.timer.Stop()
				}
				delete(c.pending, k)
			}
		}
	}

	pe, exists := c.pending[eventID]
	if !exists {
		pe = &pendingEvent{
			created: time.Now(),
		}
		pe.payload.ID = eventID
		c.pending[eventID] = pe
	}

	// Merge the partial payload into the accumulated state.
	mergePayload(&pe.payload, &payload)

	// Ensure the ID is always set from the action frame.
	if pe.payload.ID == "" {
		pe.payload.ID = eventID
	}

	// If we don't have smart detect types yet, wait for more updates.
	if len(pe.payload.SmartDetectTypes) == 0 {
		return
	}

	// Emit an immediate "early" event the first time smartDetectTypes
	// appears, so downstream consumers can act without waiting for
	// the full enrichment delay. Fields like score/end may be zero.
	if !pe.earlySent {
		pe.earlySent = true
		c.emitEarlyEvent(pe.payload, cameraNames, events)
	}

	// Start or reset the emit timer to allow more updates (score, end,
	// camera, etc.) to arrive before emitting the enriched event.
	const emitDelay = 2 * time.Second

	if pe.timer != nil {
		pe.timer.Stop()
	}
	pe.timer = time.AfterFunc(emitDelay, func() {
		c.emitPendingEvent(eventID, cameraNames, events)
	})
}

// mergePayload merges non-zero fields from src into dst.
// WebSocket update messages are partial JSON patches — only changed
// fields are present, so we overlay them onto the accumulated state.
func mergePayload(dst, src *EventPayload) {
	if src.ID != "" {
		dst.ID = src.ID
	}
	if src.Type != "" {
		dst.Type = src.Type
	}
	if src.ModelKey != "" {
		dst.ModelKey = src.ModelKey
	}
	if src.Camera != "" {
		dst.Camera = src.Camera
	}
	if len(src.SmartDetectTypes) > 0 {
		dst.SmartDetectTypes = src.SmartDetectTypes
	}
	if len(src.SmartDetectEvents) > 0 {
		dst.SmartDetectEvents = src.SmartDetectEvents
	}
	if src.Score > 0 {
		dst.Score = src.Score
	}
	if src.Start > 0 {
		dst.Start = src.Start
	}
	if src.End > 0 {
		dst.End = src.End
	}
}

// emitEarlyEvent sends an immediate SmartDetectEvent with whatever data has
// been accumulated so far. Fields like score or end may still be zero.
// The event is marked Early=true so the publisher can route it to the
// early topic. Called synchronously from processMessage under pendingMu.
func (c *Client) emitEarlyEvent(payload EventPayload, cameraNames map[string]string, events chan<- SmartDetectEvent) {
	cameraName := cameraNames[payload.Camera]
	if cameraName == "" {
		cameraName = payload.Camera
	}

	for _, detectType := range payload.SmartDetectTypes {
		if detectType != "person" && detectType != "animal" {
			continue
		}

		// Skip if an enriched event was already emitted for this
		// event+type (e.g. during a rapid re-detection cycle).
		dedupeKey := payload.ID + ":" + detectType
		if c.wasRecentlySeen(dedupeKey) {
			continue
		}

		thumbnailURL := fmt.Sprintf("%s/thumbnail/%s?w=640&h=360",
			c.proxyBaseURL, payload.ID)
		videoURL := fmt.Sprintf("%s/video/%s",
			c.proxyBaseURL, payload.ID)

		var playbackURL string
		if c.externalHost != "" {
			playbackURL = fmt.Sprintf("%s/protect/timelapse/%s?start=%d",
				c.externalHost, payload.Camera, payload.Start)
		} else {
			playbackURL = fmt.Sprintf("https://%s/protect/timelapse/%s?start=%d",
				c.host, payload.Camera, payload.Start)
		}

		event := SmartDetectEvent{
			EventID:      payload.ID,
			Type:         detectType,
			Camera:       payload.Camera,
			CameraName:   cameraName,
			Score:        payload.Score,
			Start:        payload.Start,
			End:          payload.End,
			Timestamp:    time.Now(),
			ThumbnailURL: thumbnailURL,
			VideoURL:     videoURL,
			PlaybackURL:  playbackURL,
			Early:        true,
		}

		c.logger.Info().
			Str("type", detectType).
			Str("camera", cameraName).
			Int("score", payload.Score).
			Msg("smart detection (early)")

		events <- event
	}
}

// emitPendingEvent builds and sends the SmartDetectEvent from accumulated
// data. Called by the emit timer after a short delay to allow enrichment.
func (c *Client) emitPendingEvent(eventID string, cameraNames map[string]string, events chan<- SmartDetectEvent) {
	c.pendingMu.Lock()
	pe, ok := c.pending[eventID]
	if !ok {
		c.pendingMu.Unlock()
		return
	}
	payload := pe.payload
	delete(c.pending, eventID)
	c.pendingMu.Unlock()

	cameraName := cameraNames[payload.Camera]
	if cameraName == "" {
		cameraName = payload.Camera
	}

	for _, detectType := range payload.SmartDetectTypes {
		if detectType != "person" && detectType != "animal" {
			continue
		}

		// Deduplicate: multiple detections of the same type on the same
		// event within the cooldown window are suppressed.
		dedupeKey := payload.ID + ":" + detectType
		if c.isDuplicate(dedupeKey) {
			c.logger.Debug().
				Str("event_id", payload.ID).
				Str("type", detectType).
				Str("camera", cameraName).
				Msg("skipping duplicate event")
			continue
		}

		thumbnailURL := fmt.Sprintf("%s/thumbnail/%s?w=640&h=360",
			c.proxyBaseURL, payload.ID)
		videoURL := fmt.Sprintf("%s/video/%s",
			c.proxyBaseURL, payload.ID)

		var playbackURL string
		if c.externalHost != "" {
			playbackURL = fmt.Sprintf("%s/protect/timelapse/%s?start=%d",
				c.externalHost, payload.Camera, payload.Start)
		} else {
			playbackURL = fmt.Sprintf("https://%s/protect/timelapse/%s?start=%d",
				c.host, payload.Camera, payload.Start)
		}

		event := SmartDetectEvent{
			EventID:      payload.ID,
			Type:         detectType,
			Camera:       payload.Camera,
			CameraName:   cameraName,
			Score:        payload.Score,
			Start:        payload.Start,
			End:          payload.End,
			Timestamp:    time.Now(),
			ThumbnailURL: thumbnailURL,
			VideoURL:     videoURL,
			PlaybackURL:  playbackURL,
		}

		c.logger.Info().
			Str("type", detectType).
			Str("camera", cameraName).
			Int("score", payload.Score).
			Msg("smart detection")

		events <- event
	}
}

// isDuplicate returns true if we've already seen this key recently.
// It also records the key for future checks and periodically prunes
// old entries.
func (c *Client) isDuplicate(key string) bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()

	const cooldown = 30 * time.Second

	if t, ok := c.seen[key]; ok && time.Since(t) < cooldown {
		return true
	}

	c.seen[key] = time.Now()

	// Prune old entries every so often to avoid unbounded growth.
	if len(c.seen) > 500 {
		now := time.Now()
		for k, t := range c.seen {
			if now.Sub(t) > cooldown {
				delete(c.seen, k)
			}
		}
	}

	return false
}

// wasRecentlySeen returns true if this key was recorded by isDuplicate
// within the cooldown window. Unlike isDuplicate, it does not record the
// key — used by emitEarlyEvent so that only the enriched event registers
// in the dedup map.
func (c *Client) wasRecentlySeen(key string) bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()

	const cooldown = 30 * time.Second

	t, ok := c.seen[key]
	return ok && time.Since(t) < cooldown
}

func (c *Client) applyHeaders(req *http.Request) {
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	if c.csrfToken != "" {
		req.Header.Set("X-CSRF-Token", c.csrfToken)
	}
}

// ProxyGet performs an authenticated GET to the Protect API and returns the
// raw response. The caller is responsible for closing the response body.
// Uses a client with no timeout so streaming responses are not cut short.
func (c *Client) ProxyGet(ctx context.Context, path string) (*http.Response, error) {
	c.mu.Lock()
	csrfToken := c.csrfToken
	cookies := c.cookies
	c.mu.Unlock()

	url := fmt.Sprintf("https://%s%s", c.host, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating proxy request: %w", err)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}

	resp, err := c.proxyClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxy request: %w", err)
	}
	return resp, nil
}

// Host returns the controller hostname.
func (c *Client) Host() string {
	return c.host
}

// GetRecentEvents fetches the last N smart detection events from the Protect
// REST API and returns them as SmartDetectEvents ready for publishing.
func (c *Client) GetRecentEvents(ctx context.Context, limit int) ([]SmartDetectEvent, error) {
	bootstrap, err := c.GetBootstrap(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting bootstrap: %w", err)
	}

	cameraNames := make(map[string]string)
	for _, cam := range bootstrap.Cameras {
		cameraNames[cam.ID] = cam.Name
	}

	c.mu.Lock()
	csrfToken := c.csrfToken
	cookies := c.cookies
	c.mu.Unlock()

	// Fetch more than requested because not all events have smartDetectTypes.
	// We filter down to smart detections afterward.
	fetchLimit := limit * 5
	if fetchLimit < 50 {
		fetchLimit = 50
	}

	url := fmt.Sprintf("https://%s/proxy/protect/api/events?orderDirection=DESC&limit=%d&types=smartDetectZone",
		c.host, fetchLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating events request: %w", err)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("events request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("events request failed with status %d", resp.StatusCode)
	}

	var apiEvents []APIEvent
	if err := json.NewDecoder(resp.Body).Decode(&apiEvents); err != nil {
		return nil, fmt.Errorf("decoding events: %w", err)
	}

	var events []SmartDetectEvent
	for _, ae := range apiEvents {
		for _, detectType := range ae.SmartDetectTypes {
			if detectType != "person" && detectType != "animal" {
				continue
			}

			cameraName := cameraNames[ae.Camera]
			if cameraName == "" {
				cameraName = ae.Camera
			}

			thumbnailURL := fmt.Sprintf("%s/thumbnail/%s?w=640&h=360",
				c.proxyBaseURL, ae.ID)
			videoURL := fmt.Sprintf("%s/video/%s",
				c.proxyBaseURL, ae.ID)

			var playbackURL string
			if c.externalHost != "" {
				playbackURL = fmt.Sprintf("%s/protect/timelapse/%s?start=%d",
					c.externalHost, ae.Camera, ae.Start)
			} else {
				playbackURL = fmt.Sprintf("https://%s/protect/timelapse/%s?start=%d",
					c.host, ae.Camera, ae.Start)
			}

			events = append(events, SmartDetectEvent{
				EventID:      ae.ID,
				Type:         detectType,
				Camera:       ae.Camera,
				CameraName:   cameraName,
				Score:        ae.Score,
				Start:        ae.Start,
				End:          ae.End,
				Timestamp:    time.UnixMilli(ae.Start),
				ThumbnailURL: thumbnailURL,
				VideoURL:     videoURL,
				PlaybackURL:  playbackURL,
			})

			if len(events) >= limit {
				// Reverse so oldest is first — matches chronological publish order.
				for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
					events[i], events[j] = events[j], events[i]
				}
				return events, nil
			}
		}
	}

	// Reverse so oldest is first.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}
