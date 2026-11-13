-- PNVS holds the code; local records hold binding, execution and one-time registration state.
ALTER TABLE sms_challenges ALTER COLUMN code_hash DROP NOT NULL;
ALTER TABLE sms_challenges ADD COLUMN provider text NOT NULL DEFAULT 'legacy';
ALTER TABLE sms_challenges ADD COLUMN binding_hash text NOT NULL DEFAULT '';
ALTER TABLE sms_challenges ADD COLUMN hostname text NOT NULL DEFAULT '';
ALTER TABLE sms_challenges ADD COLUMN stage text NOT NULL DEFAULT 'unavailable';
ALTER TABLE sms_challenges ADD COLUMN attempt_id text NOT NULL DEFAULT '';
ALTER TABLE sms_challenges ADD COLUMN request_hash text NOT NULL DEFAULT '';
ALTER TABLE sms_challenges ADD COLUMN provider_biz_id text NOT NULL DEFAULT '';
ALTER TABLE sms_challenges ADD COLUMN provider_request_id text NOT NULL DEFAULT '';
ALTER TABLE sms_challenges ADD COLUMN registered_user_id text REFERENCES users(id);
CREATE INDEX sms_challenges_phone_provider_idx ON sms_challenges(phone,provider,created_at DESC);
CREATE INDEX sms_challenges_expiry_idx ON sms_challenges(expires_at);
CREATE TABLE registration_consents (
 user_id text PRIMARY KEY REFERENCES users(id),terms_version integer NOT NULL,privacy_version integer NOT NULL,accepted_at timestamptz NOT NULL DEFAULT now()
);
