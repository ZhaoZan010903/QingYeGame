-- Bind completion separately from the SMS proof: a new phone must explicitly register.
ALTER TABLE sms_challenges ADD COLUMN completion_hash text NOT NULL DEFAULT '';
