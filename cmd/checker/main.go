package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StatusResponse struct {
	Reference string `json:"reference"`
	Result    string `json:"result"`
}

// for those payments which bank charged and stored the response in the map (i mean we can consider map as a bank database)
// but before seding us response back api crashed and we neve recieved the response of the charged payments
// so that is y checker at the background will check the status of those payments and update the status of those payments in the database
func checkBankStatus(referenceID string) (string, error) {
	body := fmt.Sprintf(`{"reference_id":"%s"}`, referenceID)

	resp, err := http.Post("http://localhost:9091/status", "application/json", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var statusResp StatusResponse
	err = json.NewDecoder(resp.Body).Decode(&statusResp)
	if err != nil {
		return "", err
	}

	return statusResp.Result, nil
}

//	the purpose of this function is that for those payments which map has no record this means those payments never touched the bank
//
// and we will  let checker to charge those payments again and check the status of those payments after charging them.
// This is to neccessary to implement becasue as when we have saved payment as pending and after saving when the time for bank call
// come then our server got crashed and payemnt just left as pending so bg checker will try to reolve those ......
func checkBankCharge(referenceID string, amountCents int64) (string, error) {
	body := fmt.Sprintf(`{"reference_id":"%s","amount_cents":%d}`, referenceID, amountCents)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost:9091/bank", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var bankResp StatusResponse
	err = json.NewDecoder(resp.Body).Decode(&bankResp)
	if err != nil {
		return "", err
	}

	return bankResp.Result, nil
}

func main() {
	// making a connection to the database using the pgx library
	ctx := context.Background()
	ConStr := os.Getenv("DATABASE_URL")
	if ConStr == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}
	conn, err := pgxpool.New(ctx, ConStr)
	if err != nil {
		log.Fatalf("DB ERROR: %v", err)
	}
	defer conn.Close()

	count := 0
	// in this specific query i am making sure that checker does not have conflict with those payments which r being processed by a live api for the first time
	// so we set the time that just pick those payments which r at least thirty seconds old which will make sure it is not the first time being processed payment
	// becasue we want checker to pick those pending payments which r not updated due to crash not those which r being processed for the first time
	// also we r picking unknown payments for which we retried but did not got answer due to glicth of bank even after retrying
	// and when bg checker will check the status and will still not response or no record than we will increase the checked_count and if checked_count is more than 3 than we will send those payments to human review
	rows, err := conn.Query(ctx, `SELECT id, status, amount_cents, customer_id, merchant_id FROM payments
WHERE under_review = false
  AND (status='unknown' OR (status='pending' AND updated_at < now() - interval '30 seconds'))`)
	if err != nil {
		log.Fatalf("query failed: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status, customerID, merchantID string
		var amountCents int64
		err = rows.Scan(&id, &status, &amountCents, &customerID, &merchantID)
		if err != nil {
			log.Fatalf("failed to scan row: %v", err)
		}
		count++
		log.Printf("found payment %s, status=%s, amount=%d, customer=%s, merchant=%s", id, status, amountCents, customerID, merchantID)

		result, err := checkBankStatus(id)
		if err != nil {
			log.Printf("could not check bank status for %s: %v", id, err)
			continue
		}
		log.Printf("bank says payment %s is: %s", id, result)
		// no record means that payment never reached the bank
		if result == "no_record" {
			log.Printf("payment %s never reached the bank, attempting charge now", id)
			result, err = checkBankCharge(id, amountCents)
			if err != nil {
				log.Printf("could not charge payment %s: %v", id, err)
				continue
			}
			log.Printf("bank now says payment %s is: %s", id, result)
		}
		// if payment is succeeded than this mean we have to make sure an atomic transaction will be done
		// this means that we will update the payment status to succeeded and also we will create ledger
		// entries for customer, merchant and fee account in a single transaction
		if result == "succeeded" {
			tx, err := conn.Begin(ctx)
			if err != nil {
				log.Printf("Database error: %v", err)
				continue
			}
			tag, err := tx.Exec(ctx, `UPDATE payments SET status = 'succeeded', updated_at = now() WHERE id = $1 AND status IN ('pending', 'unknown')`, id)
			if err != nil {
				log.Printf("Database error: %v", err)
				tx.Rollback(ctx)
				continue
			}
			if tag.RowsAffected() == 0 {
				log.Printf("payment %s already finalized by someone else, skipping", id)
				tx.Rollback(ctx)
				continue
			}
			// in this calculation we are taking 3% fee from the total amount and giving the rest to the merchant
			feeAccountID := "e4c75aa7-a8d5-4f50-a4c9-3d6f4dffa01d"
			feeCents := amountCents * 3 / 100
			merchantCents := amountCents - feeCents
			_, err = tx.Exec(ctx,
				`INSERT INTO ledger_entries (id, payment_id, account_id, amount_cents) VALUES
				 ($1, $2, $3, $4), ($5, $2, $6, $7), ($8, $2, $9, $10)`,
				uuid.New().String(), id, customerID, -amountCents,
				uuid.New().String(), merchantID, merchantCents,
				uuid.New().String(), feeAccountID, feeCents,
			)
			if err != nil {
				log.Printf("DB ERROR: %v", err)
				tx.Rollback(ctx)
				continue
			}
			err = tx.Commit(ctx)
			if err != nil {
				log.Printf("DB ERROR: %v", err)
				continue
			}
			log.Printf("payment %s resolved: succeeded", id)
			continue
		}

		if result == "failed" {
			tag, err := conn.Exec(ctx, `UPDATE payments SET status = 'failed', updated_at = now() WHERE id = $1 AND status IN ('pending', 'unknown')`, id)
			if err != nil {
				log.Printf("Database error: %v", err)
				continue
			}
			if tag.RowsAffected() == 0 {
				log.Printf("payment %s already finalized by someone else, skipping", id)
				continue
			}
			log.Printf("payment %s resolved: failed", id)
			continue
		}

		// result == "no_record" or anything unclear
		tag, err := conn.Exec(ctx, `UPDATE payments SET checked_count = checked_count + 1, updated_at = now() WHERE id = $1 AND status IN ('pending', 'unknown')`, id)
		if err != nil {
			log.Printf("Database error: %v", err)
			continue
		}
		if tag.RowsAffected() == 0 {
			log.Printf("payment %s already finalized by someone else, skipping", id)
			continue
		}

		var newCount int
		err = conn.QueryRow(ctx, `SELECT checked_count FROM payments WHERE id = $1`, id).Scan(&newCount)
		if err != nil {
			log.Printf("Database error: %v", err)
			continue
		}

		if newCount >= 3 {
			_, err = conn.Exec(ctx, `UPDATE payments SET under_review = true WHERE id = $1`, id)
			if err != nil {
				log.Printf("Database error: %v", err)
				continue
			}
			log.Printf("payment %s sent to human review after %d checks", id, newCount)
		} else {
			log.Printf("payment %s still unclear, checked_count now %d", id, newCount)
		}
	}
	log.Printf("checker run complete, found %d payment(s) to process", count)
}
