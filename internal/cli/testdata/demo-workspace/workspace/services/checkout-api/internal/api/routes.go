// Package api is the HTTP layer. Handlers decode a request, call the payment
// service, and encode the answer.
package api

import (
	"net/http"

	"example.com/payments-platform/services/checkout-api/internal/payments"
)

// NewRouter returns the checkout-api routes.
func NewRouter(svc *payments.PaymentService, db SQL) http.Handler {
	create := &CreateHandler{svc: svc}
	get := &GetHandler{svc: svc}
	capture := &CaptureHandler{svc: svc, db: db}
	refund := &RefundHandler{svc: svc, db: db}
	admin := &AdminHandler{db: db}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/payments", create.Serve)
	mux.HandleFunc("GET /v1/payments/{id}", get.Serve)
	mux.HandleFunc("POST /v1/payments/{id}/capture", capture.Serve)
	mux.HandleFunc("POST /v1/payments/{id}/refund", refund.Serve)
	mux.HandleFunc("GET /healthz", Health{}.Serve)
	mux.Handle("/admin/", http.StripPrefix("/admin", admin.Mux()))
	return mux
}

// Health answers the load balancer.
type Health struct{}

// Serve reports that the process is up.
func (Health) Serve(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
