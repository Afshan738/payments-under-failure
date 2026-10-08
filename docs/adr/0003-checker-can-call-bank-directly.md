# 0003: The checker can call the bank directly when status says no record

Status: accepted
Date: 2026-10-07

## Context

The checker originally only asked the bank's status endpoint. That
endpoint can only answer from what the bank already knows. For a
payment that crashed before the bank was ever contacted, the bank
genuinely has nothing on file, and status would say so forever. The
checker would just keep counting that as unclear and eventually send it
to human review, even though a human cannot resolve it either, since
nothing ever happened.

## Decision

If status comes back with no record, the checker calls the charge
endpoint itself, using the same reference. This only works safely
because, in this design, exactly one checker runs at a time, so there
is no second caller that could race it into charging twice.

## Consequences

If this project ever ran more than one checker instance at once, this
decision would need to be revisited, since two overlapping checkers
could both see no record and both charge. Noted as an open question
below rather than built, since it is outside the current scope.
