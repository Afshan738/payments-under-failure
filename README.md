# payments-under-failure

A simulated payment processor, built to demonstrate idempotency, ledger
correctness, and recovery from an injected process crash, not just to
write a charge API.

## Run it

```bash
cp .env.example .env        # then set DATABASE_URL in your shell
docker compose up -d
psql "$DATABASE_URL" -f database/schema.sql

go run ./cmd/bank           # fake bank, port 9091
go run ./cmd/api            # API, port 8080
go run ./cmd/checker        # run manually, or wire to a scheduler
```

`DATABASE_URL` is read from the environment, credentials are not
hardcoded. See `cmd/api/main.go` and `cmd/checker/main.go`.

## First-time setup

The schema starts empty. Create a merchant and a customer, then send a
charge:

```bash
curl -X POST http://localhost:8080/account \
  -d '{"account_type":"merchant","label":"Test Shop"}'
# copy the returned account_id as MERCHANT_ID

curl -X POST http://localhost:8080/account \
  -d '{"account_type":"customer","external_ref":"cust-1","owner_merchant_id":"<MERCHANT_ID>"}'
# copy the returned account_id as CUSTOMER_ID

curl -X POST http://localhost:8080/charge \
  -d '{"merchant_id":"<MERCHANT_ID>","customer_id":"<CUSTOMER_ID>","amount_cents":10000,"idempotency_key":"demo-1"}'
```

Run the last command twice with the same `idempotency_key`. The second
call returns the same `payment_id` and does not charge again.

## Results

132 process exits injected on purpose, right after a payment is saved
and before the bank is ever contacted. 132 of 132 were recovered by the
background checker, with zero duplicate payments and zero unbalanced
ledgers. Full numbers and the queries used to confirm them are in
[docs/results.md](docs/results.md).

Limit of this test: every injected crash happens before the bank call.
The case where the bank records a charge and the response is lost is
not simulated yet. See "Deliberately not built" below.

## Architecture

<img width="883" height="565" alt="image" src="https://github.com/user-attachments/assets/32b848f1-0bd9-4cb7-9543-e691915c3b97" />


## Failure scenarios handled

See the full table, all 15 edge cases, in
[docs/design.md](docs/design.md), section 5.

## Deliberately not built

Scoped out on purpose, to keep this focused on correctness under
failure first. Full reasoning in docs/design.md section 3.

- Payout, refunds, multi currency
- A customer facing UI, and webhooks
- Bank side idempotency on the charge endpoint. The simulated bank never
  records a charge and then loses the response, so the double charge
  exposure on API retries and on checker re-charges is not exercised.
  Planned: a lost-response failure mode in the bank, plus a counter that
  checks for exactly one applied charge per reference.
- An automated test suite in CI. The results above come from manual
  runs, confirmed with the SQL queries in docs/results.md.
- Cloud deployment and load testing against it

## Docs

- [docs/design.md](docs/design.md): the full design, every edge case,
  the alternatives considered and why they were not chosen, and the
  open questions I have not settled yet.
- [docs/results.md](docs/results.md): every number, and the exact
  queries used to confirm it.
- [docs/adr/](docs/adr): short, individual records of specific
  decisions, each one left as it was written, only ever superseded by
  a new one, not edited after the fact.
