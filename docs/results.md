# Results

Test setup for every number below: one local Postgres container, one API
process, one bank process, all requests sent from a single machine.
No cloud deployment, no k6, by choice, see design.md section 3.

| Test | Setup | Result |
|---|---|---|
| Idempotency, same key, concurrent | 50 concurrent requests, same idempotency key | 1 payment created, 1 bank call, 3 ledger rows, sum = 0 |
| Idempotency, same key, concurrent, larger | 200 concurrent requests, same idempotency key, run twice | 1 payment created each run, 3 ledger rows, sum = 0 each run |
| Connection safety, before fix | 1000 concurrent requests, single shared pgx.Conn | Server crash, fatal error, concurrent map writes |
| Connection safety, after fix | Same 1000 concurrent requests, pgxpool.Pool | 0 crashes, 1000 of 1000 payments correct, total time about 43 seconds |
| Retry timing, before tuning | 3 attempts, 2 second timeout, 200 to 499ms jitter | 6.4 to 7 seconds worst case, measured directly |
| Retry timing, after tuning | 2 attempts, 700ms timeout, 50 to 99ms jitter | about 1.45 to 1.5 seconds worst case, measured directly |
| Crash injection, 132 runs | Process killed right after saving pending, before the bank is ever called | 132 of 132 left correctly at pending, 0 ledger rows written at crash time |
| Checker recovery of all 132 | Checker run against the crashed payments | 121 resolved succeeded, 11 resolved failed, 0 unbalanced ledgers |
| Checker escalation | Payment with 3 real, answered, inconclusive checks | Correctly flagged under_review, confirmed a later checker run skips it entirely |

## The queries used to confirm these, not just the logs

Ledger balance, should always return 0 rows:
```sql
SELECT payment_id, SUM(amount_cents)
FROM ledger_entries
GROUP BY payment_id
HAVING SUM(amount_cents) <> 0;
```

Duplicate payments for one key, should always return 0 rows:
```sql
SELECT merchant_id, idempotency_key, COUNT(*)
FROM payments
GROUP BY merchant_id, idempotency_key
HAVING COUNT(*) > 1;
```

Crash batch outcome:
```sql
SELECT status, COUNT(*) FROM payments
WHERE idempotency_key LIKE 'crash-test-batch%'
GROUP BY status;
```
