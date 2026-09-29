# payments-under-failure

A simulation of a payment processor, built to test idempotency, ledger
correctness, and failure recovery. This is a learning project — no real
money, no real bank, no real customer data.

## What it does

A merchant sends a charge. The system saves it exactly once (idempotent,
protected against retries), calls a fake bank over HTTP, and writes the
money in a double-entry ledger only when the bank confirms success. A
payment the bank never answers is marked `unknown`, never guessed at.

## Status

- Idempotent `/account` (find-or-create by merchant + external reference)
- Idempotent `/charge` (same key + same amount returns the same payment;
  same key + different amount is rejected)
- A real HTTP call to a fake bank service, with a 2-second timeout
- All three terminal outcomes handled and verified against the live
  database: `succeeded` (ledger written, entries sum to zero), `failed`
  (no ledger), `unknown` (no ledger, never hangs)

Still to build: retries before giving up on the bank, the background
checker that resolves `unknown` payments, crash tests, load testing.

## Running locally

```
docker compose up -d
docker compose exec postgres psql -U Afshan525 -d payments-db -f /docker-entrypoint-initdb.d/schema.sql
# or, if the volume isn't wired for auto-init:
# psql "postgres://Afshan525:Afshan123@localhost:5434/payments-db" -f database/schema.sql

go run ./cmd/bank    # fake bank, port 9090
go run ./cmd/api     # API, port 8080
```
