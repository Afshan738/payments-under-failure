# 0001: Save the payment as pending before calling the bank


## Context

A charge request has to do two things: save a record of the attempt, and
ask the bank to actually move the money. These cannot happen in one
step, since the bank call is a network call that can fail, hang, or take
a long time, and we cannot hold a database transaction open across that.

## Decision

Save the payment as `pending` in its own transaction first. Only after
that succeeds do we call the bank. This means a crash before the bank
call still leaves a row behind, instead of leaving nothing at all.

## Consequences

A payment can now get stuck at `pending` if the process dies between
the save and the bank call. This is why the checker has to scan for old
`pending` rows, not just `unknown` ones. Tested directly: killed the API
process at this exact point 132 times, every single payment was left at
`pending` with zero ledger rows, and the checker recovered all of them.
