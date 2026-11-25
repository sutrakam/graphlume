package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"

	"example.com/payments-platform/services/ledger/ledger"
)

func main() {
	// The postgres driver is registered by the deployment image; the fixture
	// never connects to a database.
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	l := ledger.New(db)
	go func() { _ = ledger.NewConsumer(l).Run(context.Background()) }()

	h := ledger.NewHandler(l)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/entries/{id}", h.Entries)
	mux.HandleFunc("POST /v1/entries/{id}/reverse", h.Reverse)
	log.Fatal(http.ListenAndServe(os.Getenv("LISTEN_ADDR"), mux))
}
