package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/api"
	"github.com/ashishsinghbora/magicloder/internal/config"
	"github.com/ashishsinghbora/magicloder/internal/engine/scheduler"
	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

func setupTestServer(t *testing.T, token string) (*api.Server, storage.Store, *scheduler.Scheduler, string, func()) {
	tempDir, err := os.MkdirTemp("", "magicloder-api-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = tempDir
	cfg.DownloadDir = tempDir
	cfg.AuthToken = token

	store, err := storage.Open(filepath.Join(tempDir, "api.db"))
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}

	sched := scheduler.New(cfg, store, nil)
	_ = sched.Start()

	srv := api.NewServer(cfg, store, sched, nil)

	cleanup := func() {
		sched.Stop()
		_ = store.Close()
		_ = os.RemoveAll(tempDir)
	}

	return srv, store, sched, tempDir, cleanup
}

func TestSystemEndpoints(t *testing.T) {
	srv, _, _, _, cleanup := setupTestServer(t, "")
	defer cleanup()

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// 1. Health
	resp, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health failed: %v, status: %d", err, resp.StatusCode)
	}

	// 2. Version
	resp, err = http.Get(ts.URL + "/api/v1/version")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /version failed: %v, status: %d", err, resp.StatusCode)
	}

	// 3. Config
	resp, err = http.Get(ts.URL + "/api/v1/config")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config failed: %v, status: %d", err, resp.StatusCode)
	}
}

func TestDownloadAPILifecycle(t *testing.T) {
	srv, _, _, tempDir, cleanup := setupTestServer(t, "")
	defer cleanup()

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// 1. Create Download
	createBody := map[string]any{
		"url":      "https://example.com/file.zip",
		"filename": "custom_file.zip",
		"priority": 2,
	}
	bodyBytes, _ := json.Marshal(createBody)

	resp, err := http.Post(ts.URL+"/api/v1/downloads", "application/json", bytes.NewReader(bodyBytes))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /downloads failed: %v, status: %d", err, resp.StatusCode)
	}

	var created model.Download
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	if created.ID == "" || created.Destination == "" {
		t.Fatalf("invalid created download: %+v", &created)
	}
	if filepath.Base(created.Destination) != "custom_file.zip" {
		t.Fatalf("expected custom_file.zip, got %s", created.Destination)
	}

	// 2. List Downloads
	resp, err = http.Get(ts.URL + "/api/v1/downloads")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /downloads failed: %v", err)
	}
	var list []model.Download
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()

	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected 1 download in list, got %d", len(list))
	}

	// 3. Get Download
	resp, err = http.Get(ts.URL + "/api/v1/downloads/" + created.ID)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /downloads/{id} failed: %v", err)
	}
	var inspected model.Download
	_ = json.NewDecoder(resp.Body).Decode(&inspected)
	resp.Body.Close()

	if inspected.ID != created.ID {
		t.Fatalf("inspected mismatch")
	}

	// 4. Pause Download
	resp, err = http.Post(ts.URL+"/api/v1/downloads/"+created.ID+"/pause", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /pause failed: %v", err)
	}
	var paused model.Download
	_ = json.NewDecoder(resp.Body).Decode(&paused)
	resp.Body.Close()

	if paused.Status != model.StatusPaused {
		t.Fatalf("expected paused status, got %s", paused.Status)
	}

	// 5. Resume Download
	resp, err = http.Post(ts.URL+"/api/v1/downloads/"+created.ID+"/resume", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resume failed: %v", err)
	}
	var resumed model.Download
	_ = json.NewDecoder(resp.Body).Decode(&resumed)
	resp.Body.Close()

	if resumed.Status != model.StatusQueued {
		t.Fatalf("expected queued status on resume, got %s", resumed.Status)
	}

	// 6. Cancel Download
	resp, err = http.Post(ts.URL+"/api/v1/downloads/"+created.ID+"/cancel", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cancel failed: %v", err)
	}

	// 7. Delete Download
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/downloads/"+created.ID, nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil || delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /downloads/{id} failed: %v", err)
	}

	_ = tempDir
}

func TestAuthenticationEnforcement(t *testing.T) {
	token := "secret-token-xyz"
	srv, _, _, _, cleanup := setupTestServer(t, token)
	defer cleanup()

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// Request without token should fail
	resp, err := http.Get(ts.URL + "/api/v1/downloads")
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without token, got %d", resp.StatusCode)
	}

	// Request with invalid token should fail
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/downloads", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// Request with valid token should succeed
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", resp.StatusCode)
	}
}
