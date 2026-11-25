package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/services/risk-engine/risk"
)

func main() {
	// The postgres driver is registered by the deployment image; the fixture
	// never connects to a database.
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	scorer, flagger := risk.NewScorer(db), risk.NewFlagger(db)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/risk/score", func(w http.ResponseWriter, r *http.Request) {
		var p core.Payment
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		score, err := scorer.Score(r.Context(), p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if score >= 80 {
			_ = flagger.Flag(r.Context(), p.ID, "score above 80")
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"score": score})
	})
	mux.HandleFunc("POST /v1/risk/clear/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := flagger.Clear(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	log.Fatal(http.ListenAndServe(os.Getenv("LISTEN_ADDR"), mux))
}
