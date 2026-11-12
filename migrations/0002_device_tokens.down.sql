-- Migration: 0002_device_tokens.down.sql
-- Description: Drops device_tokens table and associated indexes

DROP TABLE IF EXISTS device_tokens CASCADE;

