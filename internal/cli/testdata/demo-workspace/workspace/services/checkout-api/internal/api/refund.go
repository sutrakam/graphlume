package api

import (
	"net/http"

	"example.com/payments-platform/services/checkout-api/internal/payments"
	"example.com/payments-platform/services/checkout-api/internal/store"
)

// RefundHandler serves POST /v1/payments/{id}/refund.
type RefundHandler struct {
	svc *payments.PaymentService
	db  SQL
}

// Serve refunds a captured payment.
func (h *RefundHandler) Serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// LAYERING VIOLATION 2 of 3: a handler reading a table directly.
	rows, err := store.Query(r.Context(), h.db, "SELECT status FROM payments WHERE id = $1", id)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	var status string
	if rows.Next() {
		_ = rows.Scan(&status)
	}
	if status != "captured" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only captured payments can be refunded"})
		return
	}
	p, err := h.svc.Refund(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
