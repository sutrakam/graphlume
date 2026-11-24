package api

import (
	"net/http"

	"example.com/payments-platform/services/checkout-api/internal/store"
)

// AdminHandler serves the operator routes. They are mounted under /admin at
// run time, so the full path is not visible in one place.
type AdminHandler struct {
	db SQL
}

// Mux returns the admin routes, relative to /admin.
func (h *AdminHandler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /purge-keys", h.PurgeKeys)
	return mux
}

// PurgeKeys deletes idempotency keys older than a day.
func (h *AdminHandler) PurgeKeys(w http.ResponseWriter, r *http.Request) {
	// LAYERING VIOLATION 3 of 3: a handler deleting rows itself.
	if _, err := store.Exec(r.Context(), h.db, "DELETE FROM idempotency_keys WHERE created_at < now() - interval '1 day'"); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
