// this i smy fake bank service that will randomly decide whether a charge request will succeed,
// fail or go silent. This is to simulate real world scenarios where banks may not always respond in a predictable manner.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
)

type BankChargeRequest struct {
	ReferenceID string `json:"reference_id"`
	AmountCents int64  `json:"amount_cents"`
}
type statusRequest struct {
	ReferenceID string `json:"reference_id"`
}

// map for temporarily storing the results of charge requests. In a real-world scenario, this would be a database.

var (
	results   = make(map[string]string)
	resultsMu sync.Mutex
)

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
		resultsMu.Lock()
		results[req.ReferenceID] = "failed"
		resultsMu.Unlock()
		fmt.Fprintf(w, `{"reference": "%s", "result": "failed"}`, req.ReferenceID)
		return
	}

	if roll < 20 {

		select {}
		// making it stuck on purpose to simulate a bank that is not responding.
		//  This will cause the request to timeout on the client side.
	}
	// remaining 80% chance will  succeeds
	resultsMu.Lock()
	results[req.ReferenceID] = "succeeded"
	resultsMu.Unlock()
	fmt.Fprintf(w, `{"reference": "%s", "result": "succeeded"}`, req.ReferenceID)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	var req statusRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// using the lock mechanism bcz we want that just one req can access the map at one time
	resultsMu.Lock()
	result, found := results[req.ReferenceID]
	if !found {
		fmt.Fprintf(w, `{"reference": "%s", "result": "no_record"}`, req.ReferenceID)
		resultsMu.Unlock()
		return
	}
	resultsMu.Unlock()
	fmt.Fprintf(w, `{"reference": "%s", "result": "%s"}`, req.ReferenceID, result)
}
func main() {
	http.HandleFunc("/bank", bankChargeRequestHandler)
	http.HandleFunc("/status", statusHandler)
	log.Println("Fake bank running on :9091")
	http.ListenAndServe(":9091", nil)
}
