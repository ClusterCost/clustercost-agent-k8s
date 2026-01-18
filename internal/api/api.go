package api

import (
	"encoding/json"
	"net/http"

	"clustercost-agent-k8s/internal/snapshot"
)

// Handler serves the agent HTTP API.
type Handler struct {
	clusterType   string
	clusterName   string
	clusterRegion string
	version       string
	agentID       string
	store         *snapshot.Store
}

// NewHandler builds a Handler bound to the snapshot store.
func NewHandler(clusterType, clusterName, clusterRegion, version, agentID string, store *snapshot.Store) *Handler {
	return &Handler{
		clusterType:   clusterType,
		clusterName:   clusterName,
		clusterRegion: clusterRegion,
		version:       version,
		agentID:       agentID,
		store:         store,
	}
}

// Register wires all API endpoints on the mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/agent/v1/health", h.health)
	mux.HandleFunc("/agent/v1/readyz", h.readyz)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status":        "ok",
		"version":       h.version,
		"clusterType":   h.clusterType,
		"clusterName":   h.clusterName,
		"clusterRegion": h.clusterRegion,
		"agentId":       h.agentID,
	})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.store.Latest(); ok {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	respondError(w, http.StatusServiceUnavailable, "snapshot not ready")
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
