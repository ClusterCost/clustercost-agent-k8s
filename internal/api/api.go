package api

import (
	"encoding/json"
	"net/http"
	"time"

	"clustercost-agent-k8s/internal/snapshot"
)

// Handler serves the agent HTTP API.
type Handler struct {
	clusterType   string
	clusterName   string
	clusterRegion string
	version       string
	store         *snapshot.Store
}

// NewHandler builds a Handler bound to the snapshot store.
func NewHandler(clusterType, clusterName, clusterRegion, version string, store *snapshot.Store) *Handler {
	return &Handler{
		clusterType:   clusterType,
		clusterName:   clusterName,
		clusterRegion: clusterRegion,
		version:       version,
		store:         store,
	}
}

// Register wires all API endpoints on the mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/agent/v1/readyz", h.readyz)
	mux.HandleFunc("/agent/v1/overview", h.overview)
	mux.HandleFunc("/agent/v1/health", h.health)
	mux.HandleFunc("/agent/v1/namespaces", h.namespaces)
	mux.HandleFunc("/agent/v1/nodes", h.nodes)
	mux.HandleFunc("/agent/v1/resources", h.resources)
	mux.HandleFunc("/agent/v1/network", h.network)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	status := "initializing"
	timestamp := time.Now().UTC()
	if snap, ok := h.store.Latest(); ok {
		status = "ok"
		timestamp = snap.Timestamp.UTC()
	}

	payload := map[string]any{
		"status":        status,
		"clusterType":   h.clusterType,
		"clusterName":   h.clusterName,
		"clusterRegion": h.clusterRegion,
		"version":       h.version,
		"timestamp":     timestamp.Format(time.RFC3339Nano),
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if snap, ok := h.store.Latest(); ok {
		payload := map[string]any{
			"status":        "ok",
			"clusterType":   h.clusterType,
			"clusterName":   h.clusterName,
			"clusterRegion": h.clusterRegion,
			"version":       h.version,
			"timestamp":     snap.Timestamp.UTC().Format(time.RFC3339Nano),
		}
		respondJSON(w, http.StatusOK, payload)
		return
	}
	payload := map[string]any{
		"status":        "initializing",
		"clusterType":   h.clusterType,
		"clusterName":   h.clusterName,
		"clusterRegion": h.clusterRegion,
		"version":       h.version,
		"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.store.Latest(); ok {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	respondError(w, http.StatusServiceUnavailable, "snapshot not ready")
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

func (h *Handler) network(w http.ResponseWriter, r *http.Request) {
	if snap, ok := h.store.Latest(); ok {
		payload := map[string]any{
			"network":   snap.Network,
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
