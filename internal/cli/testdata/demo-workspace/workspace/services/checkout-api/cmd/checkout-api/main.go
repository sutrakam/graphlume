package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	pgw "example.com/payments-platform/libs/payments-core/gateway"
	"example.com/payments-platform/services/checkout-api/internal/api"
	"example.com/payments-platform/services/checkout-api/internal/gateway"
	"example.com/payments-platform/services/checkout-api/internal/payments"
	"example.com/payments-platform/services/checkout-api/internal/store"
	"example.com/payments-platform/services/checkout-api/internal/worker"
	"example.com/payments-platform/services/ledger/ledger"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// The postgres driver is registered by the deployment image; the fixture
	// never connects to a database.
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}

	var gw pgw.Gateway = gateway.NewStripe(os.Getenv("PAYMENTS_GATEWAY_URL"))
	if os.Getenv("PAYMENTS_GATEWAY_URL") == "" {
		gw = gateway.Sandbox{}
	}

	st := store.New(db)
	svc := payments.NewService(st, gw, ledger.New(db), payments.NewNotifier())

	backoffMS, err := strconv.Atoi(os.Getenv("RETRY_BACKOFF_MS"))
	if err != nil {
		backoffMS = 2000
	}
	go worker.NewRetryWorker(svc, st, time.Duration(backoffMS)*time.Millisecond).Run(context.Background())

	handler := h2c.NewHandler(api.NewRouter(svc, db), &http2.Server{})
	log.Fatal(http.ListenAndServe(os.Getenv("LISTEN_ADDR"), handler))
}
