package compliance

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error"`
}

// Handler exposes compliance operations over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a new compliance HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes wires all compliance endpoints to the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/compliance/alerts", h.handleGetAlerts)
	mux.HandleFunc("PUT /api/v1/compliance/alerts/{id}", h.handleUpdateAlert)
}

func (h *Handler) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	statusParam := r.URL.Query().Get("status")
	if statusParam == "" {
		statusParam = string(AlertOpen)
	}

	status := AlertStatus(statusParam)
	alerts, err := h.svc.GetAlerts(r.Context(), status)
	if err != nil {
		slog.Error("failed to get alerts", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retrieve alerts")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

type updateAlertRequest struct {
	Status     string `json:"status"`
	AssignedTo string `json:"assigned_to"`
}

func (h *Handler) handleUpdateAlert(w http.ResponseWriter, r *http.Request) {
	alertID := r.PathValue("id")
	if alertID == "" {
		writeError(w, http.StatusBadRequest, "alert id is required")
		return
	}

	var req updateAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required")
		return
	}

	err := h.svc.UpdateAlertStatus(r.Context(), alertID, AlertStatus(req.Status), req.AssignedTo)
	if err != nil {
		slog.Error("failed to update alert", "error", err, "alert_id", alertID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
