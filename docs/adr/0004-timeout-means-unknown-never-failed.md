# 0004: A timeout from the bank means unknown, never failed

Status: accepted
Date: 2026-09-25

## Context

When the bank does not answer in time, there are two possible truths:
it never processed the charge, or it processed it and the answer to us
got lost. These look identical from our side, a timeout either way.

## Decision

Only an explicit decline from the bank is ever marked `failed`. A
timeout, no matter how many retries, is marked `unknown`.

## Consequences

If a timeout were marked `failed`, a merchant could tell the customer
the payment did not go through while the bank had actually already
taken the money, meaning the customer pays twice if they try another
way to pay. Marking it `unknown` instead means the system waits for the
real answer before saying anything final.
