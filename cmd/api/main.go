package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var conn *pgxpool.Pool

type ChargeRequest struct {
	MerchantID     string `json:"merchant_id"`
	CustomerID     string `json:"customer_id"`
	AmountCents    int64  `json:"amount_cents"`
	IdempotencyKey string `json:"idempotency_key"`
}
type AccountRequest struct {
	AccountType     string `json:"account_type"`
	Label           string `json:"label"`
	ExternalRef     string `json:"external_ref"`
	OwnerMerchantID string `json:"owner_merchant_id"`
}

type BankResponse struct {
	Reference string `json:"reference"`
	Result    string `json:"result"`
}

// our function for handling the account creation request
func accountHandler(w http.ResponseWriter, r *http.Request) {
	var req AccountRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var ownerID *string
	if req.OwnerMerchantID != "" {
		ownerID = &req.OwnerMerchantID
	}
	var extRef *string
	if req.ExternalRef != "" {
		extRef = &req.ExternalRef
	}
	// firstly we r trying to find that if the account with same external_ref and owner_merchant_id already exists in the database or not
	var existingAccountID string
	err = conn.QueryRow(r.Context(),
		`SELECT id FROM accounts WHERE external_ref = $1 AND owner_merchant_id = $2`,
		extRef, ownerID,
	).Scan(&existingAccountID)
	if err == nil {
		fmt.Fprintf(w, `{"account_id": "%s", "created": false}`, existingAccountID)
		return
	}
	if err != pgx.ErrNoRows {
		log.Println("DB ERROR:", err)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	accountID := uuid.New().String() //creating a new UUID for the account

	_, err = conn.Exec(r.Context(),
		`INSERT INTO accounts (id, account_type, label, external_ref, owner_merchant_id) 
		 VALUES ($1, $2, $3, $4, $5)`,
		accountID, req.AccountType, req.Label, extRef, ownerID,
	)
	if err != nil {
		log.Println("DB ERROR:", err)
		http.Error(w, "could not save account", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, `{"account_id": "%s"}`, accountID)
}

// our fucntion for calling the bank service to charge the customer
func callBankservice(ctx context.Context, referenceID string, amountCents int64) (string, error) {
	body := fmt.Sprintf(`{"reference_id":"%s","amount_cents":%d}`, referenceID, amountCents)

	var lastErr error

	for attempt := 1; attempt <= 2; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)

		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, "http://localhost:9091/bank", strings.NewReader(body))
		if err != nil {
			cancel()
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		cancel()

		if err == nil {
			defer resp.Body.Close()
			var bankResp BankResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&bankResp)
			if decodeErr != nil {
				return "", decodeErr
			}
			return bankResp.Result, nil
		}

		// no answer this time.... so we will remember the error and try again unless this was the last attempt which is the 2nd one
		lastErr = err
		log.Printf("bank call attempt %d failed: %v", attempt, err)

		if attempt < 2 {
			jitter := time.Duration(rand.Intn(50)) * time.Millisecond
			wait := 50*time.Millisecond + jitter
			time.Sleep(wait)
		}
	}

	return "", lastErr
}

// our fucntion for handling the charge request
func chargeHandler(w http.ResponseWriter, r *http.Request) {
	var req ChargeRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	start := time.Now()
	paymentID := uuid.New().String()
	var savedID string
	err = conn.QueryRow(r.Context(),
		`INSERT INTO payments (id, customer_id, merchant_id, idempotency_key, amount_cents, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending')
		 ON CONFLICT (merchant_id, idempotency_key) DO NOTHING
		 RETURNING id`,
		paymentID, req.CustomerID, req.MerchantID, req.IdempotencyKey, req.AmountCents,
	).Scan(&savedID)

	if err == nil {
		// Anyone can uncomment this to simulate a crash after saving the payment as pending but before calling the bank service.
		// if strings.HasPrefix(req.IdempotencyKey, "crash-test-") {
		// 	log.Println("SIMULATING CRASH after pending save, payment:", savedID)
		// 	os.Exit(1)
		// }
		result, bankErr := callBankservice(r.Context(), savedID, req.AmountCents)
		if bankErr != nil {
			log.Println("BANK SERVICE ERROR:", bankErr)

			_, updateErr := conn.Exec(context.Background(),
				`UPDATE payments SET status = 'unknown', updated_at = now()
				 WHERE id = $1 AND status = 'pending'`,
				savedID,
			)
			if updateErr != nil {
				log.Println("DB ERROR:", updateErr)
			}
			log.Printf("chargeHandler total duration: %v", time.Since(start))
			fmt.Fprintf(w, `{"payment_id": "%s", "amount_cents": %d, "status": "unknown"}`, savedID, req.AmountCents)
			return
		}

		if result == "succeeded" {
			tx, err := conn.Begin(r.Context())
			if err != nil {
				log.Println("DB ERROR:", err)
				http.Error(w, "could not process payment", http.StatusInternalServerError)
				return
			}
			defer tx.Rollback(r.Context())

			tag, err := tx.Exec(r.Context(),
				`UPDATE payments SET status='succeeded' WHERE id=$1 AND status='pending'`, savedID)
			if err != nil {
				log.Println("DB ERROR:", err)
				http.Error(w, "could not finalize payment", http.StatusInternalServerError)
				return
			}
			if tag.RowsAffected() == 0 {
				http.Error(w, "payment already finalized", http.StatusConflict)
				return
			}

			feeAccountID := "e4c75aa7-a8d5-4f50-a4c9-3d6f4dffa01d"
			feeCents := req.AmountCents * 3 / 100
			merchantCents := req.AmountCents - feeCents

			_, err = tx.Exec(r.Context(),
				`INSERT INTO ledger_entries (id, payment_id, account_id, amount_cents) VALUES
				 ($1, $2, $3, $4),
				 ($5, $2, $6, $7),
				 ($8, $2, $9, $10)`,
				uuid.New().String(), savedID, req.CustomerID, -req.AmountCents,
				uuid.New().String(), req.MerchantID, merchantCents,
				uuid.New().String(), feeAccountID, feeCents,
			)
			if err != nil {
				log.Println("DB ERROR:", err)
				http.Error(w, "could not write ledger", http.StatusInternalServerError)
				return
			}

			err = tx.Commit(r.Context())
			if err != nil {
				log.Println("DB ERROR:", err)
				http.Error(w, "could not finalize payment", http.StatusInternalServerError)
				return
			}

			log.Printf("chargeHandler total duration: %v", time.Since(start))
			fmt.Fprintf(w, `{"payment_id": "%s", "amount_cents": %d, "status": "succeeded"}`, savedID, req.AmountCents)
			return
		}
		if result == "failed" {
			_, err = conn.Exec(r.Context(),
				`UPDATE payments SET status = 'failed', updated_at = now()
				 WHERE id = $1 AND status = 'pending'`,
				savedID,
			)
			if err != nil {
				log.Println("DB ERROR:", err)
				http.Error(w, "could not finalize payment", http.StatusInternalServerError)
				return
			}
			log.Printf("chargeHandler total duration: %v", time.Since(start))
			fmt.Fprintf(w, `{"payment_id": "%s", "amount_cents": %d, "status": "failed"}`, savedID, req.AmountCents)
			return
		}

		fmt.Fprintf(w, `{"payment_id": "%s", "amount_cents": %d, "status": "pending", "bank_said": "%s"}`, savedID, req.AmountCents, result)
		return
	}

	if err != pgx.ErrNoRows {
		log.Println("DB ERROR:", err)
		http.Error(w, "could not save payment", http.StatusInternalServerError)
		return
	}

	var existingPaymentID, existingStatus string
	var existingAmountCents int64
	err = conn.QueryRow(r.Context(), `SELECT id,status,amount_cents FROM payments WHERE merchant_id=$1 AND idempotency_key=$2`,
		req.MerchantID, req.IdempotencyKey).Scan(&existingPaymentID, &existingStatus, &existingAmountCents)
	if err != nil {
		log.Println("DB error", err)
		http.Error(w, "could not retrieve the existing payment", http.StatusInternalServerError)
		return
	}
	if existingAmountCents != req.AmountCents {
		http.Error(w, "amount mismatch", http.StatusConflict)
		return
	}
	log.Printf("chargeHandler total duration: %v", time.Since(start))
	fmt.Fprintf(w, `{"payment_id": "%s", "status": "%s", "amount_cents": %d}`, existingPaymentID, existingStatus, existingAmountCents)
}

func main() {
	ctx := context.Background()
	var err error
	ConStr := os.Getenv("DATABASE_URL")
	if ConStr == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}
	// using a connection pool for concurrency test
	// bcz throwing the 1000 req at the same time will break just using a single connection so we will use a connection pool to handle the concurrency
	// one point i observed here is that even i sued connection pool but latency started increasing which was due to the connection pool ..
	// due to which os has to do alot of context switching which spiked our latency
	// so hence proved that concurrency is not free it cost latency

	conn, err = pgxpool.New(ctx, ConStr)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
		return
	}
	defer conn.Close()
	http.HandleFunc("/account", accountHandler)
	http.HandleFunc("/charge", chargeHandler)
	log.Println("Server is running on port 8080...")
	http.ListenAndServe(":8080", nil)
}
