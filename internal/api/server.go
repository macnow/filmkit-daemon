// Package api provides the HTTP server for filmkit-daemon.
// Serves both the REST API and the FilmKit frontend static files.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"filmkit-daemon/internal/profile"
	"filmkit-daemon/internal/ptp"
)

// Server holds the HTTP server state.
type Server struct {
	camera      *ptp.Camera
	mu          sync.Mutex // serializes all camera operations
	frontendDir string
	port        int
}

// NewServer creates a server that serves the API and frontend from frontendDir.
func NewServer(frontendDir string, port int) *Server {
	return &Server{
		camera:      ptp.NewCamera(nil),
		frontendDir: frontendDir,
		port:        port,
	}
}

// Run starts the HTTP server (blocking).
func (s *Server) Run() error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/presets", s.handlePresets)
	mux.HandleFunc("/api/presets/", s.handlePresetSlot)
	mux.HandleFunc("/api/raf/load", s.handleRAFLoad)
	mux.HandleFunc("/api/raf/profile", s.handleRAFProfile)
	mux.HandleFunc("/api/raf/reconvert", s.handleRAFReconvert)
	mux.HandleFunc("/api/raf/reconvert-raw", s.handleRAFReconvertRaw)
	mux.HandleFunc("/api/files", s.handleFileList)
	mux.HandleFunc("/api/files/", s.handleFileRoute)

	// Frontend static files
	mux.HandleFunc("/", s.handleFrontend)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("filmkit-daemon listening on http://0.0.0.0%s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      cors(mux),
		ReadTimeout:  30 * time.Minute, // large RAF uploads can take minutes
		WriteTimeout: 30 * time.Minute, // JPEG response after long conversion
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

// cors adds permissive CORS headers so the frontend can call the API
// from any origin (useful during development).
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// -- Helpers --

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// -- Handlers --

// GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	writeJSON(w, 200, map[string]any{
		"connected": s.camera.Connected(),
		"model":     s.camera.ModelName,
		"rafLoaded": s.camera.RAFLoaded,
	})
}

// POST /api/connect
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.camera.Connected() {
		writeJSON(w, 200, map[string]any{
			"connected":     true,
			"model":         s.camera.ModelName,
			"rawConversion": s.camera.SupportedProperties[0xD185],
		})
		return
	}
	if err := s.camera.Connect(); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"connected":     true,
		"model":         s.camera.ModelName,
		"rawConversion": s.camera.SupportedProperties[0xD185],
	})
}

// POST /api/disconnect
func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.camera.Disconnect()
	writeJSON(w, 200, map[string]string{"status": "disconnected"})
}

// GET /api/presets — scan all 7 preset slots from camera
func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}

	presets, err := s.camera.ScanPresets()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	type apiProp struct {
		ID    uint16 `json:"id"`
		Name  string `json:"name"`
		Value any    `json:"value"`
		Bytes []byte `json:"bytes"` // base64 in JSON
	}
	type apiPreset struct {
		Slot     int       `json:"slot"`
		Name     string    `json:"name"`
		Settings []apiProp `json:"settings"`
		UI       profile.PresetUIValues `json:"ui"`
	}

	result := make([]apiPreset, 0, len(presets))
	for _, p := range presets {
		settings := make([]apiProp, len(p.Settings))
		for i, s := range p.Settings {
			settings[i] = apiProp{ID: s.ID, Name: s.Name, Value: s.Value, Bytes: s.Bytes}
		}
		result = append(result, apiPreset{
			Slot:     p.Slot,
			Name:     p.Name,
			Settings: settings,
			UI:       profile.TranslatePresetToUI(p.Settings),
		})
	}

	writeJSON(w, 200, result)
}

// PUT /api/presets/{slot} — write a preset to a camera slot
func (s *Server) handlePresetSlot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, 405, "method not allowed")
		return
	}

	// Extract slot number from path: /api/presets/3
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/presets/"), "/")
	slot, err := strconv.Atoi(parts[0])
	if err != nil || slot < 1 || slot > 7 {
		writeError(w, 400, "invalid slot (1-7)")
		return
	}

	var body struct {
		Name     string `json:"name"`
		Settings []struct {
			ID    uint16 `json:"id"`
			Bytes []byte `json:"bytes"`
		} `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}

	settings := make([]ptp.RawProp, len(body.Settings))
	for i, s := range body.Settings {
		settings[i] = ptp.RawProp{ID: s.ID, Bytes: s.Bytes}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}

	warnings, err := s.camera.WritePreset(slot, body.Name, settings)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "warnings": warnings})
}

// POST /api/raf/load — upload a RAF file, returns JPEG
// Accepts multipart/form-data with field "file", or raw body with Content-Type=application/octet-stream.
func (s *Server) handleRAFLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}

	// Client sends RAF as a raw binary body with X-File-Size header.
	// This avoids multipart buffering — on OpenWrt /tmp is tmpfs (RAM), so
	// ParseMultipartForm spill-to-disk would still consume RAM.
	fileSizeStr := r.Header.Get("X-File-Size")
	if fileSizeStr == "" {
		writeError(w, 400, "missing X-File-Size header")
		return
	}
	rafSize, err := strconv.ParseInt(fileSizeStr, 10, 64)
	if err != nil || rafSize <= 0 {
		writeError(w, 400, "invalid X-File-Size")
		return
	}

	// Save RAF to /overlay (ext4 eMMC, not RAM) before sending to camera.
	// Streaming directly from HTTP to USB causes stalls because TCP pauses
	// make libusb emit short packets that confuse the camera's USB driver.
	// Reading from disk is continuous, keeping the USB pipe full.
	const rafCachePath = "/overlay/filmkit_raf_cache.tmp"
	tmpFile, err := os.Create(rafCachePath)
	if err != nil {
		writeError(w, 500, "creating temp file: "+err.Error())
		return
	}
	written, err := io.Copy(tmpFile, io.LimitReader(r.Body, rafSize))
	tmpFile.Close()
	if err != nil || written != rafSize {
		os.Remove(rafCachePath)
		writeError(w, 500, fmt.Sprintf("saving RAF: wrote %d of %d bytes", written, rafSize))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		os.Remove(rafCachePath)
		writeError(w, 503, "camera not connected")
		return
	}

	f, err := os.Open(rafCachePath)
	if err != nil {
		os.Remove(rafCachePath)
		writeError(w, 500, "opening cached RAF: "+err.Error())
		return
	}
	jpeg, err := s.camera.LoadRAFStream(f, rafSize)
	f.Close()
	os.Remove(rafCachePath)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
	w.WriteHeader(200)
	_, _ = w.Write(jpeg)
}

// POST /api/raf/reconvert — apply new ConversionParams to the loaded RAF, returns JPEG
func (s *Server) handleRAFReconvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}

	var params profile.ConversionParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}
	if !s.camera.RAFLoaded || s.camera.BaseProfile == nil {
		writeError(w, 409, "no RAF loaded — call /api/raf/load first")
		return
	}

	modifiedProfile := profile.PatchProfile(s.camera.BaseProfile, params)
	jpeg, err := s.camera.Reconvert(modifiedProfile)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
	w.WriteHeader(200)
	_, _ = w.Write(jpeg)
}

// handleFrontend serves the FilmKit static frontend.
// Falls back to index.html for SPA routing.
func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if s.frontendDir == "" {
		writeError(w, 404, "frontend not configured")
		return
	}

	// Clean and resolve path
	urlPath := filepath.Clean(r.URL.Path)
	if urlPath == "." || urlPath == "/" {
		urlPath = "index.html"
	}
	fullPath := filepath.Join(s.frontendDir, urlPath)

	// Serve the file if it exists
	if _, err := os.Stat(fullPath); err == nil {
		http.ServeFile(w, r, fullPath)
		return
	}

	// SPA fallback — return index.html for unknown paths
	http.ServeFile(w, r, filepath.Join(s.frontendDir, "index.html"))
}

// GET /api/raf/profile — returns the cached base profile bytes (binary).
// The frontend uses this to patch the profile locally before calling reconvert-raw.
func (s *Server) handleRAFProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.RAFLoaded || s.camera.BaseProfile == nil {
		writeError(w, 409, "no RAF loaded")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.camera.BaseProfile)))
	w.WriteHeader(200)
	_, _ = w.Write(s.camera.BaseProfile)
}

// POST /api/raf/reconvert-raw — accepts a raw patched profile (binary body), triggers conversion.
// The frontend patches the profile locally (same patchProfile logic) and POSTs raw bytes here.
func (s *Server) handleRAFReconvertRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}

	profileBytes, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || len(profileBytes) == 0 {
		writeError(w, 400, "missing profile bytes")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}
	if !s.camera.RAFLoaded {
		writeError(w, 409, "no RAF loaded — call /api/raf/load first")
		return
	}

	jpeg, err := s.camera.Reconvert(profileBytes)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
	w.WriteHeader(200)
	_, _ = w.Write(jpeg)
}

// GET /api/files — list all RAF files on the camera's SD card
func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}

	files, err := s.camera.ListRAFFiles()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	type apiFile struct {
		Handle uint32 `json:"handle"`
		Name   string `json:"name"`
		SizeMB string `json:"sizeMB"`
	}
	result := make([]apiFile, len(files))
	for i, f := range files {
		mb := fmt.Sprintf("%.1f", float64(f.SizeBytes)/1024/1024)
		result[i] = apiFile{Handle: f.Handle, Name: f.Name, SizeMB: mb}
	}
	writeJSON(w, 200, result)
}

// handleFileRoute dispatches /api/files/:handle and /api/files/:handle/thumb
func (s *Server) handleFileRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/files/"), "/"), "/")
	if len(parts) == 2 && parts[1] == "thumb" {
		s.handleFileThumb(w, r, parts[0])
	} else if len(parts) == 1 {
		s.handleFileDownload(w, r)
	} else {
		writeError(w, 404, "not found")
	}
}

// parseHandle parses a decimal or 0x-prefixed hex handle from a string.
func parseHandle(raw string) (uint32, error) {
	var v uint64
	var err error
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		v, err = strconv.ParseUint(raw[2:], 16, 32)
	} else {
		v, err = strconv.ParseUint(raw, 10, 32)
	}
	return uint32(v), err
}

// GET /api/files/:handle/thumb — return embedded JPEG thumbnail from camera
func (s *Server) handleFileThumb(w http.ResponseWriter, r *http.Request, rawHandle string) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	handle, err := parseHandle(rawHandle)
	if err != nil {
		writeError(w, 400, "invalid handle")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}
	// Thumbnails are immutable per handle — strong ETag allows 304 responses.
	etag := fmt.Sprintf(`"%d"`, handle)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	data, err := s.camera.GetThumb(handle)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=86400, immutable")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

// GET /api/files/{handle} — download a file from the camera by object handle
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}

	raw := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/files/"), "/")[0]
	handle, err := parseHandle(raw)
	if err != nil {
		writeError(w, 400, "invalid handle")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.camera.Connected() {
		writeError(w, 503, "camera not connected")
		return
	}

	data, err := s.camera.DownloadFile(handle)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", `attachment; filename="photo.RAF"`)
	w.WriteHeader(200)
	_, _ = w.Write(data)
}


