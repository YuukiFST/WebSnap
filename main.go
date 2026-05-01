package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	downloadDir     = "downloads"
	completeTTL     = 1800
	errorTTL        = 600
	processingTTL   = 1800
	orphanFileTTL          = 1800
	cleanupInterval        = 300
	maxConcurrentDownloads = 1
)

type sessionResult struct {
	Status    string `json:"status"`
	ZipPath   string `json:"-"`
	Filename  string `json:"-"`
	Error     string `json:"error,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type sessionState struct {
	Result  sessionResult
	MsgChan chan string
}

type sessionManager struct {
	mu              sync.Mutex
	sessions        map[string]*sessionState
	activeDownloads int
}

func newSessionManager() *sessionManager {
	return &sessionManager{
		sessions: make(map[string]*sessionState),
	}
}

func (sm *sessionManager) create(targetURL string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.activeDownloads >= maxConcurrentDownloads {
		return "", fmt.Errorf("limit of %d concurrent downloads reached", maxConcurrentDownloads)
	}

	sessionID := uuid.New().String()
	sm.sessions[sessionID] = &sessionState{
		Result: sessionResult{
			Status:    "processing",
			CreatedAt: time.Now().Unix(),
		},
		MsgChan: make(chan string, 100),
	}
	sm.activeDownloads++
	return sessionID, nil
}

func (sm *sessionManager) get(id string) (*sessionState, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.sessions[id]
	return s, ok
}

func (sm *sessionManager) purge(id string) {
	sm.mu.Lock()
	s, ok := sm.sessions[id]
	if ok {
		delete(sm.sessions, id)
	}
	sm.mu.Unlock()

	if s != nil && s.Result.ZipPath != "" {
		os.Remove(s.Result.ZipPath)
	}
	rawDir := fmt.Sprintf("%s/%s", downloadDir, id)
	os.RemoveAll(rawDir)
}

func (sm *sessionManager) runDownload(id, url string) {
	defer func() {
		sm.mu.Lock()
		sm.activeDownloads--
		sm.mu.Unlock()
	}()

	s, _ := sm.get(id)
	if s == nil {
		return
	}

	workDir := fmt.Sprintf("%s/%s", downloadDir, id)
	zipPath := fmt.Sprintf("%s/%s.zip", downloadDir, id)

	logFn := func(msg string) {
		select {
		case s.MsgChan <- msg:
		default:
		}
	}

	d := NewWebsiteDownloader(url, workDir, logFn)

	if ok := bm.Healthy(); !ok {
		logFn("> Browser died — restarting...")
		if restartErr := bm.Restart(); restartErr != nil {
			logFn(fmt.Sprintf("> Fatal: browser restart failed: %s", restartErr))
			sm.mu.Lock()
			s.Result = sessionResult{
				Status:    "error",
				CreatedAt: time.Now().Unix(),
				Error:     restartErr.Error(),
			}
			sm.mu.Unlock()
			os.RemoveAll(workDir)
			return
		}
		logFn("> Browser restarted")
	}

	tabCtx, tabCancel := bm.NewTab()
	defer tabCancel()

	if err := d.Process(tabCtx); err != nil {
		logFn(fmt.Sprintf("> Error: %s", err))
		sm.mu.Lock()
		s.Result = sessionResult{
			Status:    "error",
			CreatedAt: time.Now().Unix(),
			Error:     err.Error(),
		}
		sm.mu.Unlock()
		os.RemoveAll(workDir)
		os.Remove(zipPath)
		return
	}

	logFn("Creating ZIP file...")
	siteName := getSiteName(url)
	zipFilename := siteName + ".zip"
	if err := zipDirectory(workDir, zipPath); err != nil {
		logFn(fmt.Sprintf("> Error creating ZIP: %s", err))
		sm.mu.Lock()
		s.Result = sessionResult{
			Status:    "error",
			CreatedAt: time.Now().Unix(),
			Error:     err.Error(),
		}
		sm.mu.Unlock()
		return
	}

	os.RemoveAll(workDir)

	logFn("Download ready!")

	sm.mu.Lock()
	s.Result = sessionResult{
		Status:    "complete",
		ZipPath:   zipPath,
		Filename:  zipFilename,
		CreatedAt: time.Now().Unix(),
	}
	sm.mu.Unlock()
}

func (sm *sessionManager) cleanup() {
	for {
		time.Sleep(cleanupInterval * time.Second)
		now := time.Now().Unix()

		sm.mu.Lock()
		var toPurge []string
		for id, s := range sm.sessions {
			age := now - s.Result.CreatedAt
			switch s.Result.Status {
			case "complete":
				if age > completeTTL {
					toPurge = append(toPurge, id)
				}
			case "error":
				if age > errorTTL {
					toPurge = append(toPurge, id)
				}
			case "processing":
				if age > processingTTL {
					toPurge = append(toPurge, id)
				}
			}
		}
		sm.mu.Unlock()

		for _, id := range toPurge {
			log.Printf("> Session %s removed (expired)", id[:8])
			sm.purge(id)
		}

		sm.cleanupOrphans()
		runtime.GC()
	}
}

func (sm *sessionManager) cleanupOrphans() {
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		return
	}

	sm.mu.Lock()
	knownIDs := make(map[string]bool)
	for id := range sm.sessions {
		knownIDs[id] = true
	}
	sm.mu.Unlock()

	now := time.Now()
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime()).Seconds()
		if age < orphanFileTTL {
			continue
		}

		name := entry.Name()
		base := strings.TrimSuffix(name, ".zip")
		if knownIDs[base] {
			continue
		}

		path := fmt.Sprintf("%s/%s", downloadDir, name)
		if entry.IsDir() {
			os.RemoveAll(path)
			log.Printf("> Removed orphan directory: %s", name)
		} else {
			os.Remove(path)
			log.Printf("> Removed orphan file: %s", name)
		}
	}
}

var (
	sm   *sessionManager
	tmpl *template.Template
)

var bm *BrowserManager

func handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl.Execute(w, nil)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"status": "ok",
	}

	sm.mu.Lock()
	info["sessions"] = len(sm.sessions)
	sm.mu.Unlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	info["rss_mb"] = float64(m.Alloc) / (1024 * 1024)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func handleStartDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "URL is required"})
		return
	}

	sessionID, err := sm.create(body.URL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	go sm.runDownload(sessionID, body.URL)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"session_id": sessionID})
}

func handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	s, ok := sm.get(sessionID)
	if !ok || s == nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "data: > Session not found\n\n")
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
		return
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	deadline := time.Now().Add(30 * time.Minute)

	for {
		if time.Now().After(deadline) {
			fmt.Fprintf(w, "data: > Connection closed due to inactivity\n\n")
			fmt.Fprintf(w, "event: done\ndata: timeout\n\n")
			flusher.Flush()
			return
		}

		select {
		case msg, ok := <-s.MsgChan:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()

			sm.mu.Lock()
			status := s.Result.Status
			sm.mu.Unlock()

			if status == "complete" || status == "error" {
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", status)
				flusher.Flush()
				return
			}
		case <-time.After(30 * time.Second):
			sm.mu.Lock()
			status := s.Result.Status
			sm.mu.Unlock()

			if status == "complete" || status == "error" {
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", status)
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	s, ok := sm.get(sessionID)
	if !ok || s == nil {
		http.Error(w, "File not ready", http.StatusNotFound)
		return
	}

	sm.mu.Lock()
	status := s.Result.Status
	zipPath := s.Result.ZipPath
	filename := s.Result.Filename
	sm.mu.Unlock()

	if status != "complete" || zipPath == "" {
		http.Error(w, "File not ready", http.StatusNotFound)
		return
	}

	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		sm.purge(sessionID)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, zipPath)

	go func() {
		time.Sleep(5 * time.Minute)
		sm.purge(sessionID)
	}()
}

func main() {
	var err error
	tmpl, err = template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Failed to parse template: %v", err)
	}

	os.MkdirAll(downloadDir, 0755)
	cleanDownloadDir(downloadDir)

	sm = newSessionManager()
	go sm.cleanup()

	bm, err = NewBrowserManager()
	if err != nil {
		log.Fatalf("Failed to start browser: %v", err)
	}
	log.Println("Browser started (warm)")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down browser...")
		bm.Shutdown()
		os.Exit(0)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /start-download", handleStartDownload)
	mux.HandleFunc("GET /stream/{session_id}", handleStream)
	mux.HandleFunc("GET /download-file/{session_id}", handleDownloadFile)

	port := os.Getenv("PORT")
	if port == "" {
		port = "5001"
	}

	log.Printf("Server started on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func cleanDownloadDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		os.RemoveAll(dir + "/" + entry.Name())
	}
}
