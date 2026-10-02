package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5"
)

func main() {
	// making a connection to the database using the pgx library
	ctx := context.Background()
	ConStr := "postgres://Afshan525:Afshan123@localhost:5434/payments-db"
	conn, err := pgx.Connect(ctx, ConStr)
	if err != nil {
		log.Fatalf("DB ERROR:", err)
	}
	defer conn.Close(ctx)

	count := 0

	rows, err := conn.Query(ctx, `SELECT id,status,amount_cents from payments where status='unknown' OR (status='pending' AND updated_at<now()-interval '30 seconds')`)
	if err != nil {
		log.Fatalf("query failed", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, status string
		var amountCents int64
		err = rows.Scan(&id, &status, &amountCents)
		if err != nil {
			log.Fatalf("failed to scan row", err)

		}
		count++
		log.Printf("found payment %s, status=%s, amount=%d", id, status, amountCents)

	}
	log.Printf("checker run complete, found %d payment(s) to process", count)
}
