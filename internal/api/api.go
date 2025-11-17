package api

import (
	"encoding/json"
	"net/http"
	"time"

	"clustercost-agent-k8s/internal/snapshot"
)

// Handler serves the agent HTTP API.
type Handler struct {
	clusterID string
	store     *snapshot.Store
}

// NewHandler builds a Handler bound to the snapshot store.
func NewHandler(clusterID string, store *snapshot.Store) *Handler {
	return &Handler{
		clusterID: clusterID,
		store:     store,
	}
}

// Register wires all API endpoints on the mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/agent/v1/health", h.health)
	mux.HandleFunc("/agent/v1/namespaces", h.namespaces)
	mux.HandleFunc("/agent/v1/nodes", h.nodes)
	mux.HandleFunc("/agent/v1/resources", h.resources)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if snap, ok := h.store.Latest(); ok {
		payload := map[string]any{
			"status":    "ok",
			"clusterId": h.clusterID,
			"timestamp": snap.Timestamp.UTC().Format(time.RFC3339Nano),
		}
		respondJSON(w, http.StatusOK, payload)
		return
	}
	payload := map[string]any{
		"status":    "initializing",
		"clusterId": h.clusterID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *Handler) namespaces(w http.ResponseWriter, r *http.Request) {
	if snap, ok := h.store.Latest(); ok {
		payload := map[string]any{
			"items":     snap.Namespaces,
			"timestamp": snap.Timestamp.UTC().Format(time.RFC3339Nano),
		}
		respondJSON(w, http.StatusOK, payload)
		return
	}
	respondError(w, http.StatusServiceUnavailable, "snapshot not ready")
}

func (h *Handler) nodes(w http.ResponseWriter, r *http.Request) {
	if snap, ok := h.store.Latest(); ok {
		payload := map[string]any{
			"items":     snap.Nodes,
			"timestamp": snap.Timestamp.UTC().Format(time.RFC3339Nano),
		}
		respondJSON(w, http.StatusOK, payload)
		return
	}
	respondError(w, http.StatusServiceUnavailable, "snapshot not ready")
}

func (h *Handler) resources(w http.ResponseWriter, r *http.Request) {
	if snap, ok := h.store.Latest(); ok {
		payload := map[string]any{
			"snapshot":  snap.Resources,
			"timestamp": snap.Timestamp.UTC().Format(time.RFC3339Nano),
		}
		respondJSON(w, http.StatusOK, payload)
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
