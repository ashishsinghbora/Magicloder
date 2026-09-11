package events_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/events"
	"github.com/gorilla/websocket"
)

func TestHubBroadcastAndReceive(t *testing.T) {
	hub := events.NewHub()
	ts := httptest.NewServer(http.HandlerFunc(hub.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	// Connect 3 clients
	var conns []*websocket.Conn
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("failed to dial websocket client %d: %v", i, err)
		}
		defer conn.Close()
		conns = append(conns, conn)
	}

	// Allow server to register subscribers
	time.Sleep(20 * time.Millisecond)
	if hub.SubscriberCount() != 3 {
		t.Fatalf("expected 3 subscribers, got %d", hub.SubscriberCount())
	}

	// Broadcast an event
	testEvent := &events.Event{
		Type:           events.EventJobProgress,
		DownloadID:     "dl-ws-1",
		CompletedBytes: 500,
		TotalSize:      1000,
	}
	hub.Broadcast(testEvent)

	// Verify all 3 clients receive it
	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Add(1)
		go func(idx int, c *websocket.Conn) {
			defer wg.Done()
			_ = c.SetReadDeadline(time.Now().Add(1 * time.Second))
			_, message, err := c.ReadMessage()
			if err != nil {
				t.Errorf("client %d read error: %v", idx, err)
				return
			}
			var received events.Event
			if err := json.Unmarshal(message, &received); err != nil {
				t.Errorf("client %d json unmarshal error: %v", idx, err)
				return
			}
			if received.Type != events.EventJobProgress || received.DownloadID != "dl-ws-1" {
				t.Errorf("client %d event mismatch: %+v", idx, received)
			}
		}(i, conn)
	}
	wg.Wait()
}

func TestHubSlowClientNonBlocking(t *testing.T) {
	hub := events.NewHub()
	ts := httptest.NewServer(http.HandlerFunc(hub.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	// Connect slow client that doesn't read
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)

	// Flood with 200 events rapidly
	start := time.Now()
	for i := 0; i < 200; i++ {
		hub.Broadcast(&events.Event{
			Type:           events.EventJobProgress,
			DownloadID:     "dl-flood",
			CompletedBytes: int64(i * 10),
			TotalSize:      2000,
		})
	}
	elapsed := time.Since(start)

	// Flooding 200 events must complete immediately without waiting for slow client
	if elapsed > 100*time.Millisecond {
		t.Fatalf("hub broadcast blocked by slow client: took %v", elapsed)
	}
}
