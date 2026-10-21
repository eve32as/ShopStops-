package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	dispatchStreamSubprotocol = "shopstops-admin-v1"
	dispatchStreamQueueSize   = 16
	maxDispatchStreamClients  = 100
)

type DriverLocationUpdate struct {
	Type       string    `json:"type"`
	RouteID    string    `json:"routeId"`
	DriverRef  string    `json:"driverRef"`
	ObservedAt time.Time `json:"observedAt"`
}

type dispatchStreamHub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan DriverLocationUpdate
}

func newDispatchStreamHub() *dispatchStreamHub {
	return &dispatchStreamHub{subscribers: make(map[uint64]chan DriverLocationUpdate)}
}

func (h *dispatchStreamHub) subscribe() (<-chan DriverLocationUpdate, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subscribers) >= maxDispatchStreamClients {
		return nil, nil, false
	}
	h.nextID++
	id := h.nextID
	updates := make(chan DriverLocationUpdate, dispatchStreamQueueSize)
	h.subscribers[id] = updates
	return updates, func() {
		h.mu.Lock()
		delete(h.subscribers, id)
		h.mu.Unlock()
	}, true
}

func (h *dispatchStreamHub) publish(update DriverLocationUpdate) {
	update.Type = "driver.location.updated"
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, updates := range h.subscribers {
		select {
		case updates <- update:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- update:
			default:
			}
		}
	}
}

func (s *Server) streamAdminDispatchEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	updates, unsubscribe, ok := s.dispatchStreams.subscribe()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "too many dispatch event streams")
		return
	}
	defer unsubscribe()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:        []string{dispatchStreamSubprotocol},
		// The OIDC gateway enforces the configured Origin allowlist.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	if conn.Subprotocol() != dispatchStreamSubprotocol {
		_ = conn.Close(websocket.StatusPolicyViolation, "required subprotocol missing")
		return
	}

	ctx := conn.CloseRead(r.Context())
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
			payload, err := json.Marshal(update)
			if err != nil {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				return
			}
		case <-keepalive.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
