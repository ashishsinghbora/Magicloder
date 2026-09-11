package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/config"
	"github.com/ashishsinghbora/magicloder/internal/engine/scheduler"
	"github.com/ashishsinghbora/magicloder/internal/engine/uploader"
	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/security"
	"github.com/ashishsinghbora/magicloder/internal/storage"
	"github.com/ashishsinghbora/magicloder/internal/version"
	"github.com/google/uuid"
)

// Server coordinates the HTTP REST API.
type Server struct {
	cfg       *config.Config
	store     storage.Store
	sched     *scheduler.Scheduler
	tusClient *uploader.TusClient
	server    *http.Server
	startTime time.Time
	wsHandler http.HandlerFunc
}

// NewServer initializes the API server.
func NewServer(cfg *config.Config, store storage.Store, sched *scheduler.Scheduler, tusClient *uploader.TusClient) *Server {
	if tusClient == nil {
		tusClient = uploader.NewTusClient(store)
	}
	return &Server{
		cfg:       cfg,
		store:     store,
		sched:     sched,
		tusClient: tusClient,
		startTime: time.Now(),
	}
}

// SetWebSocketHandler registers the WebSocket event handler.
func (s *Server) SetWebSocketHandler(h http.HandlerFunc) {
	s.wsHandler = h
}

// Router configures API routes with middleware.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// System routes
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	mux.HandleFunc("GET /api/v1/config", s.handleConfig)

	// Downloads routes
	mux.HandleFunc("GET /api/v1/downloads", s.handleListDownloads)
	mux.HandleFunc("POST /api/v1/downloads", s.handleCreateDownload)
	mux.HandleFunc("GET /api/v1/downloads/{id}", s.handleGetDownload)
	mux.HandleFunc("POST /api/v1/downloads/{id}/pause", s.handlePauseDownload)
	mux.HandleFunc("POST /api/v1/downloads/{id}/resume", s.handleResumeDownload)
	mux.HandleFunc("POST /api/v1/downloads/{id}/cancel", s.handleCancelDownload)
	mux.HandleFunc("POST /api/v1/downloads/{id}/retry", s.handleRetryDownload)
	mux.HandleFunc("DELETE /api/v1/downloads/{id}", s.handleDeleteDownload)

	// Uploads routes
	mux.HandleFunc("GET /api/v1/uploads", s.handleListUploads)
	mux.HandleFunc("POST /api/v1/uploads", s.handleCreateUpload)
	mux.HandleFunc("GET /api/v1/uploads/{id}", s.handleGetUpload)
	mux.HandleFunc("DELETE /api/v1/uploads/{id}", s.handleDeleteUpload)

	// WebSocket route
	if s.wsHandler != nil {
		mux.HandleFunc("GET /api/v1/events", s.wsHandler)
	}

	handler := CORSMiddleware(mux)
	handler = AuthMiddleware(s.cfg, handler)
	handler = RecoverMiddleware(handler)

	return handler
}

// Start listens and serves on configured BindAddr.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.BindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", s.cfg.BindAddr, err)
	}

	s.server = &http.Server{
		Handler:      s.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s.server.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// --- Handler Implementations ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"uptime_seconds": int64(time.Since(s.startTime).Seconds()),
		"timestamp":      time.Now().UTC(),
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":    version.Version,
		"git_commit": version.GitCommit,
		"build_date": version.BuildDate,
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data_dir":                     s.cfg.DataDir,
		"download_dir":                 s.cfg.DownloadDir,
		"bind_addr":                    s.cfg.BindAddr,
		"remote_enabled":               s.cfg.RemoteEnabled,
		"max_concurrent_downloads":     s.cfg.MaxConcurrentDownloads,
		"default_workers_per_download": s.cfg.DefaultWorkersPerDownload,
	})
}

type createDownloadReq struct {
	URL            string `json:"url"`
	Filename       string `json:"filename,omitempty"`
	DestinationDir string `json:"destination_dir,omitempty"`
	MaxConnections int    `json:"max_connections,omitempty"`
	Priority       int    `json:"priority,omitempty"`
	ExpectedHash   string `json:"expected_hash,omitempty"`
}

func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	var req createDownloadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if _, err := security.ValidateURLScheme(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	baseDir := s.cfg.DownloadDir
	if req.DestinationDir != "" {
		baseDir = req.DestinationDir
	}

	filename := req.Filename
	if filename == "" {
		filename = security.FilenameFromURL(req.URL)
	}
	filename = security.SanitizeFilename(filename)

	finalPath, err := security.ResolveSafeDestination(baseDir, filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid destination: %v", err))
		return
	}

	finalPath, err = security.AllocateNonCollidingPath(finalPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	id := uuid.New().String()
	tempPath := filepath.Join(filepath.Dir(finalPath), fmt.Sprintf("%s.%s.part", filepath.Base(finalPath), id))

	maxConn := s.cfg.DefaultWorkersPerDownload
	if req.MaxConnections > 0 {
		maxConn = req.MaxConnections
	}

	dl := &model.Download{
		ID:             id,
		URL:            req.URL,
		Destination:    finalPath,
		TemporaryPath:  tempPath,
		TotalSize:      -1,
		CompletedBytes: 0,
		Status:         model.StatusQueued,
		MaxConnections: maxConn,
		Priority:       req.Priority,
		ExpectedHash:   req.ExpectedHash,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	dlClone := dl.Clone()
	if err := s.sched.Submit(dl); err != nil {
		if errors.Is(err, scheduler.ErrDuplicateJob) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, dlClone)
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	statusFilter := model.DownloadStatus(r.URL.Query().Get("status"))
	list, err := s.store.ListDownloads(r.Context(), statusFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = make([]*model.Download, 0)
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dl, err := s.store.GetDownload(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "download not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dl)
}

func (s *Server) handlePauseDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sched.Pause(id); err != nil {
		if errors.Is(err, scheduler.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "download not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dl, _ := s.store.GetDownload(r.Context(), id)
	writeJSON(w, http.StatusOK, dl)
}

func (s *Server) handleResumeDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sched.Resume(id); err != nil {
		if errors.Is(err, scheduler.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "download not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dl, _ := s.store.GetDownload(r.Context(), id)
	writeJSON(w, http.StatusOK, dl)
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sched.Cancel(id, false); err != nil {
		if errors.Is(err, scheduler.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "download not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dl, _ := s.store.GetDownload(r.Context(), id)
	writeJSON(w, http.StatusOK, dl)
}

func (s *Server) handleRetryDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.sched.Retry(id); err != nil {
		if errors.Is(err, scheduler.ErrJobNotFound) {
			writeError(w, http.StatusNotFound, "download not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dl, _ := s.store.GetDownload(r.Context(), id)
	writeJSON(w, http.StatusOK, dl)
}

func (s *Server) handleDeleteDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deleteFiles := r.URL.Query().Get("delete_files") == "true"

	dl, err := s.store.GetDownload(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "download not found")
		return
	}

	_ = s.sched.Remove(id)
	if deleteFiles {
		_ = os.Remove(dl.TemporaryPath)
		_ = os.Remove(dl.Destination)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Upload handlers

type createUploadReq struct {
	SourcePath  string `json:"source_path"`
	TusEndpoint string `json:"tus_endpoint"`
	Metadata    string `json:"metadata,omitempty"`
}

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	var req createUploadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if _, err := security.ValidateURLScheme(req.TusEndpoint); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid tus endpoint: %v", err))
		return
	}

	fp, size, err := uploader.ComputeFingerprint(req.SourcePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid source file: %v", err))
		return
	}

	id := uuid.New().String()
	up := &model.Upload{
		ID:              id,
		SourcePath:      req.SourcePath,
		TusEndpoint:     req.TusEndpoint,
		TotalSize:       size,
		UploadedBytes:   0,
		Status:          model.UploadQueued,
		FileFingerprint: fp,
		Metadata:        req.Metadata,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if err := s.store.CreateUpload(r.Context(), up); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Trigger async upload execution
	go func() {
		_ = s.tusClient.UploadFile(context.Background(), up, nil)
	}()

	writeJSON(w, http.StatusCreated, up)
}

func (s *Server) handleListUploads(w http.ResponseWriter, r *http.Request) {
	statusFilter := model.UploadStatus(r.URL.Query().Get("status"))
	list, err := s.store.ListUploads(r.Context(), statusFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = make([]*model.Upload, 0)
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	up, err := s.store.GetUpload(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "upload not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, up)
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	up, err := s.store.GetUpload(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "upload not found")
		return
	}

	if up.UploadURL != "" {
		_ = s.tusClient.CancelUpload(r.Context(), nil, up.UploadURL)
	}
	_ = s.store.DeleteUpload(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}
