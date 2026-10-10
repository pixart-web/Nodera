-- The public certificate chain is not secret; it is stored with the
-- certificate record. The PRIVATE key is never stored here: key_secret_key
-- references an AES-GCM encrypted entry in internal/secrets.
ALTER TABLE certificates ADD COLUMN cert_pem TEXT;
ALTER TABLE certificates ADD COLUMN subject_names TEXT[] NOT NULL DEFAULT '{}';
