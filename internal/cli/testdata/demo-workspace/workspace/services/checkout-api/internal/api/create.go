package api

import (
	"encoding/json"
	"net/http"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/services/checkout-api/internal/payments"
)

// CreateHandler serves POST /v1/payments.
type CreateHandler struct {
	svc *payments.PaymentService
}

// Serve creates a payment.
func (h *CreateHandler) Serve(w http.ResponseWriter, r *http.Request) {
	var p core.Payment
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	created, err := h.svc.Create(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// GetHandler serves GET /v1/payments/{id}.
type GetHandler struct {
	svc *payments.PaymentService
}

// Serve loads a payment.
func (h *GetHandler) Serve(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
