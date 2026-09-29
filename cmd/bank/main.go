// this i smy fake bank service that will randomly decide whether a charge request will succeed,
// fail or go silent. This is to simulate real world scenarios where banks may not always respond in a predictable manner.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
)

type BankChargeRequest struct {
	ReferenceID string `json:"reference_id"`
	AmountCents int64  `json:"amount_cents"`
}

func bankChargeRequestHandler(w http.ResponseWriter, r *http.Request) {
	var req BankChargeRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// most important part of this fake bank is to randomly decide whether the charge request will succeed, fail or go silent.
	// This is to simulate real world scenarios where banks may not always respond in a predictable manner.
	roll := rand.Intn(100) // gives a random number from 0 to 99

	if roll < 10 {
		fmt.Fprintf(w, `{"reference": "%s", "result": "failed"}`, req.ReferenceID)
		return
	}

	if roll < 20 {

		select {}
	}

	// remaining 80% chance will  succeeds
	fmt.Fprintf(w, `{"reference": "%s", "result": "succeeded"}`, req.ReferenceID)
}

func main() {
	http.HandleFunc("/bank", bankChargeRequestHandler)
	log.Println("Fake bank running on :9090")
	http.ListenAndServe(":9090", nil)
}
