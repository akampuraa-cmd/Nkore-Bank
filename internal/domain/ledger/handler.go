package ledger

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

type errorResponse struct {
	Error string `json:"error"`
}

// Handler exposes general-ledger operations over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a new ledger HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes wires all ledger endpoints to the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/gl/journal-entries", h.handlePostJournalEntry)
	mux.HandleFunc("GET /api/v1/gl/trial-balance", h.handleGetTrialBalance)
	mux.HandleFunc("GET /api/v1/gl/accounts", h.handleListAccounts)
}

// --- request/response DTOs ---

type postJournalEntryRequest struct {
	ReferenceNumber string             `json:"reference_number"`
	Description     string             `json:"description"`
	PostedBy        string             `json:"posted_by"`
	FiscalPeriod    string             `json:"fiscal_period"`
	IdempotencyKey  string             `json:"idempotency_key"`
	Entries         []glEntryRequest   `json:"entries"`
}

type glEntryRequest struct {
	GLAccountID   string `json:"gl_account_id"`
	Amount        string `json:"amount"`
	EntryType     string `json:"entry_type"`
	EffectiveDate string `json:"effective_date"`
}

func (h *Handler) handlePostJournalEntry(w http.ResponseWriter, r *http.Request) {
	var req postJournalEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ReferenceNumber == "" || req.Description == "" || req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "reference_number, description, and idempotency_key are required")
		return
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "at least one entry is required")
		return
	}

	je := &JournalEntry{
		ReferenceNumber: req.ReferenceNumber,
		Description:     req.Description,
		PostedBy:        req.PostedBy,
		FiscalPeriod:    req.FiscalPeriod,
		IdempotencyKey:  req.IdempotencyKey,
	}

	var entries []*GLEntry
	for _, e := range req.Entries {
		amt, err := decimal.NewFromString(e.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid amount: "+e.Amount)
			return
		}

		effectiveDate := time.Now().UTC()
		if e.EffectiveDate != "" {
			parsed, err := time.Parse("2006-01-02", e.EffectiveDate)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid effective_date: "+e.EffectiveDate)
				return
			}
			effectiveDate = parsed
		}

		entries = append(entries, &GLEntry{
			GLAccountID:   e.GLAccountID,
			Amount:        amt,
			EntryType:     e.EntryType,
			EffectiveDate: effectiveDate,
		})
	}

	if err := h.svc.PostJournalEntry(r.Context(), je, entries); err != nil {
		slog.Error("failed to post journal entry", "error", err)
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":               je.ID,
		"reference_number": je.ReferenceNumber,
		"status":           "posted",
	})
}

func (h *Handler) handleGetTrialBalance(w http.ResponseWriter, r *http.Request) {
	balances, err := h.svc.GetTrialBalance(r.Context())
	if err != nil {
		slog.Error("failed to get trial balance", "error", err)
		// Still return the balances with a warning if there is an imbalance.
		if balances != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"balances": balances,
				"warning":  err.Error(),
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get trial balance")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"balances": balances})
}

func (h *Handler) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.svc.repo.ListGLAccounts(r.Context())
	if err != nil {
		slog.Error("failed to list GL accounts", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list accounts")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
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
