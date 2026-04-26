package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub manages WebSocket subscriptions per task_id.
type Hub struct {
	mu    sync.RWMutex
	subs  map[int64]map[*websocket.Conn]bool
	conns map[*websocket.Conn]bool
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{
		subs:  make(map[int64]map[*websocket.Conn]bool),
		conns: make(map[*websocket.Conn]bool),
	}
}

type wsMessage struct {
	Action  string  `json:"action"`
	TaskIDs []int64 `json:"task_ids,omitempty"`
}

// HandleWS is the gin handler for WebSocket connections.
func (h *Hub) HandleWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	h.mu.Lock()
	h.conns[conn] = true
	h.mu.Unlock()

	defer func() {
		h.removeConn(conn)
		conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg wsMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			_ = conn.WriteJSON(gin.H{"error": "invalid JSON"})
			continue
		}

		switch msg.Action {
		case "subscribe":
			h.mu.Lock()
			for _, tid := range msg.TaskIDs {
				if h.subs[tid] == nil {
					h.subs[tid] = make(map[*websocket.Conn]bool)
				}
				h.subs[tid][conn] = true
			}
			h.mu.Unlock()
			_ = conn.WriteJSON(gin.H{"action": "subscribed", "task_ids": msg.TaskIDs})

		case "unsubscribe":
			h.mu.Lock()
			for _, tid := range msg.TaskIDs {
				if h.subs[tid] != nil {
					delete(h.subs[tid], conn)
					if len(h.subs[tid]) == 0 {
						delete(h.subs, tid)
					}
				}
			}
			h.mu.Unlock()
			_ = conn.WriteJSON(gin.H{"action": "unsubscribed", "task_ids": msg.TaskIDs})

		case "ping":
			_ = conn.WriteJSON(gin.H{"action": "pong"})
		}
	}
}

func (h *Hub) removeConn(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, conn)
	for tid, conns := range h.subs {
		delete(conns, conn)
		if len(conns) == 0 {
			delete(h.subs, tid)
		}
	}
}

// Broadcast sends a message to all subscribers of a task.
func (h *Hub) Broadcast(taskID int64, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	h.mu.RLock()
	subscribers := make([]*websocket.Conn, 0, len(h.subs[taskID]))
	for conn := range h.subs[taskID] {
		subscribers = append(subscribers, conn)
	}
	h.mu.RUnlock()

	var dead []*websocket.Conn
	for _, conn := range subscribers {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			dead = append(dead, conn)
		}
	}

	if len(dead) > 0 {
		for _, conn := range dead {
			h.removeConn(conn)
		}
	}
}
