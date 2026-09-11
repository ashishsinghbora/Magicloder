package events

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 5 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	clientChanSize = 64
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Origin checked in API middleware
	},
}

// Client represents an active WebSocket subscriber.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// Hub manages WebSocket subscriber connections and event broadcasting.
type Hub struct {
	mu           sync.RWMutex
	clients      map[*Client]bool
	metricsMu    sync.Mutex
	lastBytes    map[string]int64
	lastTimes    map[string]time.Time
	rollingSpeed map[string]float64
}

// NewHub initializes an event Hub.
func NewHub() *Hub {
	return &Hub{
		clients:      make(map[*Client]bool),
		lastBytes:    make(map[string]int64),
		lastTimes:    make(map[string]time.Time),
		rollingSpeed: make(map[string]float64),
	}
}

// SubscriberCount returns active connection count.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends an event to all connected WebSocket clients non-blockingly.
func (h *Hub) Broadcast(ev *Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	// Compute speed & ETA if DownloadID is present
	if ev.DownloadID != "" && ev.TotalSize > 0 {
		h.computeMetrics(ev)
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			// Client channel full -> drop frame to prevent stalling engine
		}
	}
}

func (h *Hub) computeMetrics(ev *Event) {
	h.metricsMu.Lock()
	defer h.metricsMu.Unlock()

	now := ev.Timestamp
	prevBytes, hasPrev := h.lastBytes[ev.DownloadID]
	prevTime := h.lastTimes[ev.DownloadID]

	if hasPrev && now.After(prevTime) {
		deltaSec := now.Sub(prevTime).Seconds()
		if deltaSec >= 0.5 {
			deltaBytes := float64(ev.CompletedBytes - prevBytes)
			instSpeed := deltaBytes / deltaSec
			if instSpeed < 0 {
				instSpeed = 0
			}

			// Exponential moving average (alpha = 0.3)
			prevSpeed := h.rollingSpeed[ev.DownloadID]
			speed := 0.3*instSpeed + 0.7*prevSpeed
			h.rollingSpeed[ev.DownloadID] = speed
			h.lastBytes[ev.DownloadID] = ev.CompletedBytes
			h.lastTimes[ev.DownloadID] = now
		}
	} else {
		h.lastBytes[ev.DownloadID] = ev.CompletedBytes
		h.lastTimes[ev.DownloadID] = now
	}

	speed := h.rollingSpeed[ev.DownloadID]
	ev.SpeedBytesPerSec = speed

	if speed > 0 && ev.TotalSize > ev.CompletedBytes {
		remainingBytes := ev.TotalSize - ev.CompletedBytes
		ev.ETASeconds = int64(float64(remainingBytes) / speed)
	}
}

// ServeHTTP handles incoming WebSocket upgrade requests.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, clientChanSize),
	}

	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.mu.Lock()
		delete(c.hub.clients, c)
		c.hub.mu.Unlock()
		close(c.send)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(msg)
			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
