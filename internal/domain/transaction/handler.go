package transaction

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// Handler exposes transaction operations over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a new transaction HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes registers all transaction-related routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/transactions/deposit", h.handleDeposit)
	mux.HandleFunc("POST /api/v1/transactions/withdraw", h.handleWithdraw)
	mux.HandleFunc("POST /api/v1/transactions/transfer", h.handleTransfer)
	mux.HandleFunc("GET /api/v1/transactions/{id}", h.handleGetTransaction)
	mux.HandleFunc("GET /api/v1/accounts/{id}/statement", h.handleGetStatement)
}

func (h *Handler) handleDeposit(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), tracer, "handler.Deposit")
	defer span.End()

	var req DepositRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.svc.Deposit(ctx, &req)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), tracer, "handler.Withdraw")
	defer span.End()

	var req WithdrawalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.svc.Withdraw(ctx, &req)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleTransfer(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), tracer, "handler.Transfer")
	defer span.End()

	var req TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.svc.Transfer(ctx, &req)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), tracer, "handler.GetTransaction")
	defer span.End()

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "transaction id is required")
		return
	}

	resp, err := h.svc.GetTransaction(ctx, id)
	if err != nil {
		if errors.Is(err, ErrTransactionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to retrieve transaction")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleGetStatement(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), tracer, "handler.GetStatement")
	defer span.End()

	accountID := r.PathValue("id")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "account id is required")
		return
	}

	span.SetAttributes(attribute.String("account_id", accountID))

	q := r.URL.Query()

	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		from = time.Now().UTC().AddDate(0, -1, 0) // default: last 30 days
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		to = time.Now().UTC()
	}

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("size"))
	if size < 1 || size > 100 {
		size = 50
	}

	entries, err := h.svc.GetStatement(ctx, accountID, from, to, page, size)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve statement")
		return
	}

	writeJSON(w, http.StatusOK, entries)
}

// ---------- HTTP response helpers ----------

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrAccountNotActive):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrSameAccount):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrTransactionNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}
