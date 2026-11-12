-- Migration: 0002_device_tokens.up.sql
-- Description: Creates device_tokens table for Push Notification Gateway (FCM, APNs, WebPush)

CREATE TABLE IF NOT EXISTS device_tokens (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform VARCHAR(16) NOT NULL,
    token TEXT NOT NULL,
    app_bundle_id VARCHAR(128) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_device_tokens_user_active ON device_tokens(user_id, is_active);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_device_tokens_user_token ON device_tokens(user_id, token);
