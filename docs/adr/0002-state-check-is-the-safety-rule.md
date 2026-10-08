# 0002: The state check in the UPDATE is the safety rule, the 30 second wait is only an optimization

Status: accepted
Date: 2026-10-03

## Context

The checker and a live request could, in theory, both try to finalize
the same payment at the same time. The first idea was to protect
against this by only letting the checker touch a payment once it has
been sitting for 30 seconds, on the theory that a live request would
have already finished by then.

## Decision

The 30 second wait stays, but it is not what makes this safe. The real
protection is `UPDATE payments SET status = 'succeeded' WHERE id = $1
AND status IN ('pending','unknown')`. Only one writer can ever make
that update match a row. The other gets zero rows changed and backs
off doing nothing.

## Consequences

Even if the 30 second number turns out to be wrong, or a crash happens
in a way nobody designed for, the guarded update still holds. Proved
this by hand first, on a practice table, with two open database
sessions racing the same row, before trusting it in the real code.
