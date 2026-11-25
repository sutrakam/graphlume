package api

import (
	"net/http"

	"example.com/payments-platform/services/checkout-api/internal/payments"
	"example.com/payments-platform/services/checkout-api/internal/store"
)

// CaptureHandler serves POST /v1/payments/{id}/capture.
type CaptureHandler struct {
	svc *payments.PaymentService
	db  SQL
}

// Serve captures a payment.
func (h *CaptureHandler) Serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := h.svc.Capture(r.Context(), id, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, err)
		return
	}
	// LAYERING VIOLATION 1 of 3: a handler running SQL itself.
	if _, err := store.Exec(r.Context(), h.db, "UPDATE payments SET captured_at = now() WHERE id = $1", id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}
