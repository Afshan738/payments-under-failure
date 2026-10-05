package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StatusResponse struct {
	Reference string `json:"reference"`
	Result    string `json:"result"`
}

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

func main() {
	// making a connection to the database using the pgx library
	ctx := context.Background()
	ConStr := "postgres://Afshan525:Afshan123@localhost:5434/payments-db"
	conn, err := pgxpool.New(ctx, ConStr)
	if err != nil {
		log.Fatalf("DB ERROR: %v", err)
	}
	defer conn.Close()

	count := 0

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
