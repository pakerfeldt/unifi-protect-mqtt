package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog"

	"github.com/pa/unifi-protect-mqtt/internal/protect"
)

// Server serves auth-free thumbnail and video proxy endpoints backed by
// the authenticated Protect client.
type Server struct {
	client *protect.Client
	server *http.Server
	logger *zerolog.Logger
}

// NewServer creates a new proxy server.
func NewServer(listenAddr string, client *protect.Client, logger *zerolog.Logger) *Server {
	s := &Server{
		client: client,
		logger: logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /thumbnail/{eventID}", s.handleThumbnail)
	mux.HandleFunc("GET /video/{eventID}", s.handleVideo)

	s.server = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	return s
}

// ListenAndServe starts the HTTP server. It blocks until the server is
// shut down or encounters an error.
func (s *Server) ListenAndServe() error {
	s.logger.Info().Str("addr", s.server.Addr).Msg("proxy server listening")
	err := s.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("proxy server: %w", err)
}

// Shutdown gracefully shuts down the proxy server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// handleThumbnail proxies event thumbnail images from the Protect API.
//
//	GET /thumbnail/{eventID}?w=640&h=360
func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	if eventID == "" {
		http.Error(w, "missing event ID", http.StatusBadRequest)
		return
	}

	// Build the upstream Protect API path.
	path := fmt.Sprintf("/proxy/protect/api/events/%s/thumbnail", eventID)
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}

	resp, err := s.client.ProxyGet(r.Context(), path)
	if err != nil {
		s.logger.Error().Err(err).Str("event_id", eventID).Msg("thumbnail proxy error")
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		http.Error(w, "auth error", resp.StatusCode)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleVideo proxies video clip exports from the Protect API.
// The event ID is used to look up camera and time range from the events API.
//
//	GET /video/{eventID}
func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	if eventID == "" {
		http.Error(w, "missing event ID", http.StatusBadRequest)
		return
	}

	// First, fetch the event details to get camera ID and time range.
	eventPath := fmt.Sprintf("/proxy/protect/api/events/%s", eventID)
	eventResp, err := s.client.ProxyGet(r.Context(), eventPath)
	if err != nil {
		s.logger.Error().Err(err).Str("event_id", eventID).Msg("video event lookup error")
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	defer eventResp.Body.Close()

	if eventResp.StatusCode != http.StatusOK {
		s.logger.Error().Int("status", eventResp.StatusCode).Str("event_id", eventID).Msg("event lookup failed")
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	var event struct {
		Camera string `json:"camera"`
		Start  int64  `json:"start"`
		End    int64  `json:"end"`
	}
	body, err := io.ReadAll(eventResp.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}

	// The events API may return a single object or an array.
	// Try single object first; if that fails, try array.
	if err := parseJSON(body, &event); err != nil {
		s.logger.Error().Err(err).Str("event_id", eventID).Msg("failed to parse event")
		http.Error(w, "event parse error", http.StatusInternalServerError)
		return
	}

	if event.Camera == "" || event.Start == 0 {
		http.Error(w, "incomplete event data", http.StatusNotFound)
		return
	}

	// If the event is still in progress (End == 0), use a default duration
	// of 10 seconds from the start.
	end := event.End
	if end == 0 {
		end = event.Start + 10000
	}

	// Proxy the video export from the Protect API.
	videoPath := fmt.Sprintf("/proxy/protect/api/video/export?camera=%s&start=%d&end=%d&channel=0",
		event.Camera, event.Start, end)

	videoResp, err := s.client.ProxyGet(r.Context(), videoPath)
	if err != nil {
		s.logger.Error().Err(err).Str("event_id", eventID).Msg("video proxy error")
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	defer videoResp.Body.Close()

	if videoResp.StatusCode == http.StatusUnauthorized || videoResp.StatusCode == http.StatusForbidden {
		http.Error(w, "auth error", videoResp.StatusCode)
		return
	}

	if videoResp.StatusCode != http.StatusOK {
		s.logger.Error().Int("status", videoResp.StatusCode).Str("event_id", eventID).Msg("video export failed")
		http.Error(w, fmt.Sprintf("video export failed: %d", videoResp.StatusCode), http.StatusBadGateway)
		return
	}

	contentType := videoResp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "video/mp4"
	}

	w.Header().Set("Content-Type", contentType)
	if cl := videoResp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	io.Copy(w, videoResp.Body)
}

// parseJSON tries to unmarshal data as a single JSON object.
func parseJSON(data []byte, v any) error {
	// Trim whitespace to detect arrays.
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// Array response — not expected for single event lookup, but handle
		// gracefully by returning an error.
		return fmt.Errorf("unexpected array response")
	}

	return json.Unmarshal(data, v)
}
