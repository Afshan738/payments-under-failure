# Payment Processor Simulation — Design Doc

Author: Afshan Qasim
Status: partially implemented. Where this doc and the code differ, the
ADRs in docs/adr/ and the results in docs/results.md describe what was
actually built.
Last updated: 2026-10-08

## 1. Context

I am building this to actually understand, hands on, how a real payment
system has to think about failure, not just write an API that charges a
card on the happy path. The real problem a payment system has to solve
is not "take the money", it is "know the truth about whether the money
moved, even when the system talking to you lies, goes silent, or your
own server dies in the middle of finding out."

## 2. Goals

- A merchant can send a charge and get back one, and only one, real
  payment, even if their own request times out and they retry.
- Money is tracked as a proper double entry ledger, every payment's
  entries must sum to zero, no exceptions.
- The system survives its own process crashing at the worst possible
  moment, and recovers without creating duplicate money or losing a
  payment permanently.
- Every claim in this doc and the results is something I actually
  measured or ran, not something I assumed would be true.

## 3. Non goals, by my own choice

This project is scoped to prove correctness under failure, not to be a
finished product. I am choosing, on purpose, not to build:

- Payout to the merchant, refunds, multiple currencies.
- A customer facing UI, and webhooks. The merchant is expected to poll
  for status, the same way I built it to be checked.
- An automated CI test suite that runs on every push. Every result in
  this project came from a test I ran and recorded by hand or with a
  small script, not from an automated pipeline.
- Deploying to GCP and running k6 against it. I decided correctness
  under local, controlled failure comes first, and deployment is a
  separate, later concern, not something that proves anything about
  the design itself.

None of these were assigned to me, they are choices I made about where
my time was worth spending for what this project is trying to prove.

## 4. Proposed design

### 4.1 The three pieces

- API (`cmd/api`): accepts a charge, saves it, calls the bank, finalizes
  the result.
- Fake bank (`cmd/bank`): stands in for a real, external bank we do not
  control. Answers succeed, decline, or goes silent, on purpose.
- Checker (`cmd/checker`): a background process that finds payments
  stuck in an unresolved state and resolves them.

### 4.2 Data model

- `accounts`: a small, growing table of customer, merchant, and fee
  rows. A customer belongs to exactly one merchant, found or created by
  an external reference the merchant sends us, so the same customer
  always maps to the same row across multiple orders.
- `payments`: one row per charge attempt. Status is one of pending,
  succeeded, failed, unknown. Unique on merchant plus idempotency key,
  so a retry can never create a second row.
- `ledger_entries`: the actual money movement, written only when a
  payment becomes succeeded, in the same database transaction as the
  status change. Never edited, only added to.

### 4.3 The payment lifecycle

A charge is saved as pending first, in its own transaction, before the
bank is ever called (see ADR 0001). The bank is then called with a
timeout and a small number of retries. What the bank says decides the
outcome:

- A clear yes becomes succeeded, and in the same transaction, the three
  ledger rows are written, money out of the customer, money to the
  merchant, the fee, which must sum to exactly zero.
- A clear no becomes failed. No ledger rows are ever written for a
  failed payment.
- No answer, after retries, becomes unknown. Never failed, see ADR 0004.

### 4.4 The checker

Runs on its own, separately from any live request. Looks for payments
that are unknown, or pending for more than 30 seconds (see ADR 0002 for
why 30 seconds is a safety margin, not the actual guarantee). For each
one, it asks the bank's status endpoint first. If the bank has a real
answer, the checker finalizes it, using the same guarded update and
ledger pattern as the API. If the bank has no record at all, the
checker calls the charge endpoint itself (see ADR 0003). If the answer
stays unclear across three real, answered checks, the payment is
flagged for human review and the checker never touches it again.

## 5. Edge cases, and how each one is actually handled

| # | What happens | What the system does |
|---|---|---|
| 1 | Process crashes before saving anything | Nothing exists yet. A retry with the same key is a clean, new attempt. |
| 2 | Process crashes after saving pending, before calling the bank | Payment is stuck at pending. The checker finds it after 30 seconds and resolves it. Tested 132 times, 132 recovered, zero ledger corruption. |
| 3 | Same key, same amount, sent twice | Returns the same payment, no second bank call, no duplicate row. Tested at 50 and 200 concurrent requests, always exactly one payment created. |
| 4 | Same key, different amount | Rejected outright, treated as a merchant side mistake, never silently processed. |
| 5 | Two requests with the same new key arrive at the exact same moment | The database's own unique constraint lets only one insert win, the other is told to use the existing payment. |
| 6 | Bank says yes | Status becomes succeeded and the ledger is written together, in one transaction. |
| 7 | Bank says no | Status becomes failed, no ledger entries, ever. |
| 8 | Bank never answers, even after retries | Status becomes unknown, never failed, because the bank may have actually taken the money and we just never heard back. |
| 9 | Bank answers late, after the payment is already marked unknown | Still allowed to resolve, unknown can still become succeeded or failed later. |
| 10 | Bank said yes, but the process crashes before we save that | The checker asks the bank again later and finds the same yes, then finalizes it correctly. |
| 11 | The checker and a live request try to finalize the same payment | Only one update can match the guarded WHERE clause, the other affects zero rows and does nothing further. |
| 12 | A payment never reaches the bank at all, due to a crash right after saving pending | Status check alone would say no record forever. The checker instead calls the bank directly for this one case, see ADR 0003. |
| 13 | The checker's own call to the bank goes silent | Originally hung forever, the same bug already fixed once in the API's bank call, reintroduced in a new function. Fixed by giving this call the same timeout protection. |
| 14 | A payment stays genuinely unclear after three real checks | Flagged for human review, and the checker is coded to never touch it again, confirmed by running it a second time and seeing it correctly skip. |
| 15 | 1000 requests arrive at once, sharing a single raw database connection | The server crashed entirely, fatal error, concurrent map writes, pgx.Conn is not safe for that. Fixed with a connection pool, then the same 1000 requests ran cleanly with zero crashes. |

## 6. Alternatives considered

**A state check inside the UPDATE, instead of locking the row first.**
I considered explicitly locking a payment row before reading it, the
way you would reserve a seat before deciding what to do with it. I
chose the guarded UPDATE instead, because it does both the check and
the write in one atomic step, there is no gap between "look" and
"decide" for another writer to sneak into. Proved this works under real
concurrent access by hand, with two database sessions open at once,
before trusting it anywhere in the actual code.

**A separate checker process, instead of retrying forever inside the
API.** I could have made the API keep retrying the bank internally
until it got an answer, instead of giving up after a short time and
handing the problem to a background process. I chose the separate
checker because a customer standing at checkout cannot wait minutes for
an answer, the measured worst case for a live request is about one and
a half seconds, and anything stuck past that needs a completely
different, patient, background process instead of making a person wait
for it.

**Bank side idempotency, instead of trusting the call pattern.** I
considered making the fake bank itself refuse to double process the
same reference. I traced through the actual order of calls instead,
the checker always asks status before ever asking to charge, and only
one checker ever runs at a time, and concluded the double charge this
would protect against cannot actually happen given how the system is
built right now. I am keeping this as a real, open risk rather than
pretending it is fully solved, see the open questions below.

## 7. Failure modes and risks

- A crash can happen at any line of code, not only the two points I
  deliberately tested. I tested the two points that matter most for
  money, right after saving pending, and I am treating the rest as
  covered by the same transaction and guarded update patterns, not as
  individually proven.
- The fake bank's behavior is my own assumption about what an
  unreliable bank looks like. A real bank may behave in ways this
  simulation does not cover.
- The checker currently assumes it is the only one running. If that
  assumption is ever broken, the no record fallback in ADR 0003 stops
  being safe.

## 8. Testing plan, and what was actually run

- Idempotency under concurrency: fired 50 and then 200 identical
  requests at once, confirmed exactly one payment and one balanced
  ledger each time, by querying the database directly, not by trusting
  the API's response.
- A real concurrency bug, found by testing, not by reading documentation
  first: 1000 concurrent requests against a single shared connection
  crashed the whole server. Fixed, then reran the same 1000 requests
  with zero crashes and all 1000 payments correct.
- Crash injection: killed the API process, on purpose, 132 times, right
  after a payment is saved and before the bank is ever called. Every
  single time, the payment was left at pending with zero ledger rows,
  and the checker later resolved all of them, 121 succeeded, 11 failed,
  zero unbalanced ledgers.
- Retry timing: measured, not assumed. The first version of the retry
  logic was 6.4 to 7 seconds worst case, measured with real timing logs,
  too slow for a checkout. Tuned it down and measured the new worst
  case at about 1.5 seconds.

All exact numbers and the queries used to get them are in
docs/results.md.

## 9. Open questions

- What does a real bank's status endpoint actually guarantee, is it
  always as fresh and complete as this simulation assumes.
- If this system ever ran more than one checker at a time, the no
  record fallback in ADR 0003 would need real protection against two
  checkers charging the same stuck payment. Not built, noted honestly
  as a gap.
- For a payment the checker resolves well after the original request
  ended, there is currently no way to tell the merchant or the customer
  it changed, since there are no webhooks. Whether a processor should
  resolve these automatically in the background, or push that
  responsibility back to the merchant to retry themselves, is a real
  trade off I have not settled, and want to ask about directly.
