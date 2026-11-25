package ledger

import (
	"encoding/json"
	"net/http"

	core "example.com/payments-platform/libs/payments-core"
)

// Handler exposes the ledger over HTTP.
type Handler struct {
	ledger *Ledger
}

// NewHandler returns the ledger's HTTP handlers.
func NewHandler(l *Ledger) *Handler { return &Handler{ledger: l} }

// Entries lists the entries for a payment.
func (h *Handler) Entries(w http.ResponseWriter, r *http.Request) {
	rows, err := h.ledger.db.QueryContext(r.Context(),
		`SELECT account, direction, amount_cents FROM ledger_entries WHERE payment_id = $1`, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type entry struct {
		Account   string `json:"account"`
		Direction string `json:"direction"`
		Cents     int64  `json:"cents"`
	}
	var out []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Account, &e.Direction, &e.Cents); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, e)
	}
	_ = json.NewEncoder(w).Encode(out)
}

// Reverse books the opposite entries for a payment.
func (h *Handler) Reverse(w http.ResponseWriter, r *http.Request) {
	var p core.Payment
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.ID = r.PathValue("id")
	if err := h.ledger.Reverse(r.Context(), p); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
