-- Money is always a whole number of cents. we never use decimals for money.
-- One currency only, to keep the project small.

CREATE TABLE accounts (
  id                UUID PRIMARY KEY,
  account_type      TEXT NOT NULL CHECK (account_type IN ('customer', 'merchant', 'fee')),
  label             TEXT,
  -- external_ref + owner_merchant_id let a merchant reuse the same customer
  -- across multiple orders, instead of a new "person" being created every time.
  -- Only customer accounts may have these set; merchant and fee accounts never do.
  external_ref      TEXT,
  owner_merchant_id UUID REFERENCES accounts(id),
  UNIQUE (owner_merchant_id, external_ref),
  CONSTRAINT owner_ref_only_for_customers CHECK (
    (account_type = 'customer' AND owner_merchant_id IS NOT NULL AND external_ref IS NOT NULL)
    OR
    (account_type != 'customer' AND owner_merchant_id IS NULL AND external_ref IS NULL)
  )
);

CREATE TABLE payments (
  id              UUID PRIMARY KEY,
  customer_id     UUID NOT NULL REFERENCES accounts(id),
  merchant_id     UUID NOT NULL REFERENCES accounts(id),
  idempotency_key TEXT NOT NULL,
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  status          TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','succeeded','failed','unknown')),
  under_review    BOOLEAN NOT NULL DEFAULT false,
  checked_count   INT NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (merchant_id, idempotency_key)
);

CREATE TABLE ledger_entries (
  id           UUID PRIMARY KEY,
  payment_id   UUID NOT NULL REFERENCES payments(id),
  account_id   UUID NOT NULL REFERENCES accounts(id),
  amount_cents BIGINT NOT NULL CHECK (amount_cents <> 0),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (payment_id, account_id)
);

-- Seed: one shared fee account. There is only ever one, since it represents
-- our own company's earnings bucket, not a per-payment or per-person account.
INSERT INTO accounts (id, account_type, label)
VALUES (gen_random_uuid(), 'fee', 'Processor Fee');

-- This must ALWAYS return zero rows.
-- Every payment's ledger entries must add up to zero.
--
-- SELECT payment_id, SUM(amount_cents)
-- FROM ledger_entries
-- GROUP BY payment_id
-- HAVING SUM(amount_cents) <> 0;