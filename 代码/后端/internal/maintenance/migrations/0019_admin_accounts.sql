-- Administrator profile revisions are independent of member nickname review/cooldown.
ALTER TABLE users ADD COLUMN admin_profile_version bigint NOT NULL DEFAULT 1 CHECK(admin_profile_version > 0);

-- Only keyed request fingerprints and non-sensitive response projections are retained.
CREATE TABLE admin_account_requests (
  actor_id text NOT NULL REFERENCES users(id),
  operation text NOT NULL CHECK(operation IN ('create','password')),
  request_id text NOT NULL,
  request_hash text NOT NULL CHECK(length(request_hash)=64),
  response jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(actor_id,operation,request_id)
);
