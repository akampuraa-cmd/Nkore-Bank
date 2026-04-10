package interest

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type errorResponse struct {
	Error string `json:"error"`
}

// Handler exposes interest operations over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a new interest HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes wires all interest endpoints to the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/interest/accrue", h.handleAccrue)
	mux.HandleFunc("POST /api/v1/interest/post", h.handlePost)
}

type accrueRequest struct {
	Date string `json:"date"`
}

func (h *Handler) handleAccrue(w http.ResponseWriter, r *http.Request) {
	var req accrueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	date := time.Now().UTC()
	if req.Date != "" {
		parsed, err := time.Parse("2006-01-02", req.Date)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid date format, expected YYYY-MM-DD")
			return
		}
		date = parsed
	}

	if err := h.svc.AccrueDaily(r.Context(), date); err != nil {
		slog.Error("failed to accrue interest", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to accrue interest")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "accrued",
		"date":   date.Format("2006-01-02"),
	})
}

type postRequest struct {
	ThroughDate string `json:"through_date"`
}

func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	var req postRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	throughDate := time.Now().UTC()
	if req.ThroughDate != "" {
		parsed, err := time.Parse("2006-01-02", req.ThroughDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid through_date format, expected YYYY-MM-DD")
			return
		}
		throughDate = parsed
	}

	if err := h.svc.PostAccruedInterest(r.Context(), throughDate); err != nil {
		slog.Error("failed to post accrued interest", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to post accrued interest")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "posted",
		"through_date": throughDate.Format("2006-01-02"),
	})
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
