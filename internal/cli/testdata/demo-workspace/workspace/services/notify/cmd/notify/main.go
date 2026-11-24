package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"example.com/payments-platform/services/notify/notify"
)

func main() {
	go func() { _ = notify.NewConsumer(notify.LogMailer{}).Run(context.Background()) }()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	log.Fatal(http.ListenAndServe(os.Getenv("LISTEN_ADDR"), mux))
}
