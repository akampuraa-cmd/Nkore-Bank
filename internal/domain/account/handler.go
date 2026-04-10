package account

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/telemetry"
)

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Handler exposes account operations over HTTP.
type Handler struct {
	svc     *Service
	tracer  oteltrace.Tracer
	metrics *telemetry.BankingMetrics
}

// NewHandler creates a new account HTTP handler.
func NewHandler(svc *Service, metrics *telemetry.BankingMetrics) *Handler {
	return &Handler{
		svc:     svc,
		tracer:  otel.Tracer("account"),
		metrics: metrics,
	}
}

// RegisterRoutes wires all account endpoints to the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/accounts", h.handleCreateAccount)
	mux.HandleFunc("GET /api/v1/accounts/{id}", h.handleGetAccount)
	mux.HandleFunc("GET /api/v1/accounts/{id}/balance", h.handleGetBalance)
	mux.HandleFunc("POST /api/v1/accounts/{id}/freeze", h.handleFreezeAccount)
	mux.HandleFunc("POST /api/v1/accounts/{id}/close", h.handleCloseAccount)
}

func (h *Handler) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), h.tracer, "Handler.CreateAccount")
	defer span.End()

	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, span, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
		return
	}

	resp, err := h.svc.CreateAccount(ctx, &req)
	if err != nil {
		h.handleServiceError(w, span, err)
		return
	}

	h.recordMetric(ctx, "create")
	span.SetAttributes(attribute.String("account.id", resp.ID))
	h.writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), h.tracer, "Handler.GetAccount")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("account.id", id))

	acct, err := h.svc.GetAccount(ctx, id)
	if err != nil {
		h.handleServiceError(w, span, err)
		return
	}

	h.recordMetric(ctx, "get")
	h.writeJSON(w, http.StatusOK, acct)
}

func (h *Handler) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), h.tracer, "Handler.GetBalance")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("account.id", id))

	resp, err := h.svc.GetBalance(ctx, id)
	if err != nil {
		h.handleServiceError(w, span, err)
		return
	}

	h.recordMetric(ctx, "get_balance")
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleFreezeAccount(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), h.tracer, "Handler.FreezeAccount")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("account.id", id))

	if err := h.svc.FreezeAccount(ctx, id); err != nil {
		h.handleServiceError(w, span, err)
		return
	}

	h.recordMetric(ctx, "freeze")
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "frozen"})
}

func (h *Handler) handleCloseAccount(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), h.tracer, "Handler.CloseAccount")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("account.id", id))

	if err := h.svc.CloseAccount(ctx, id); err != nil {
		h.handleServiceError(w, span, err)
		return
	}

	h.recordMetric(ctx, "close")
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

// --- helpers ---

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, span oteltrace.Span, status int, message, code string) {
	span.SetStatus(codes.Error, message)
	span.RecordError(errors.New(message))
	h.writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func (h *Handler) handleServiceError(w http.ResponseWriter, span oteltrace.Span, err error) {
	switch {
	case errors.Is(err, ErrAccountNotFound):
		h.writeError(w, span, http.StatusNotFound, "account not found", "ACCOUNT_NOT_FOUND")
	case errors.Is(err, ErrInvalidInput):
		h.writeError(w, span, http.StatusBadRequest, err.Error(), "INVALID_INPUT")
	case errors.Is(err, ErrAccountNotActive):
		h.writeError(w, span, http.StatusConflict, "account is not active", "ACCOUNT_NOT_ACTIVE")
	case errors.Is(err, ErrVersionConflict):
		h.writeError(w, span, http.StatusConflict, "concurrent modification detected", "VERSION_CONFLICT")
	case errors.Is(err, ErrNonZeroBalance):
		h.writeError(w, span, http.StatusConflict, "account has non-zero balance", "NON_ZERO_BALANCE")
	case errors.Is(err, ErrInsufficientFunds):
		h.writeError(w, span, http.StatusConflict, "insufficient funds", "INSUFFICIENT_FUNDS")
	default:
		slog.Error("unhandled error", "error", err)
		h.writeError(w, span, http.StatusInternalServerError, "internal server error", "INTERNAL_ERROR")
	}
}

func (h *Handler) recordMetric(ctx context.Context, operation string) {
	if h.metrics != nil {
		h.metrics.AccountOperations.Add(ctx, 1,
			otelmetric.WithAttributes(attribute.String("operation", operation)),
		)
	}
}
