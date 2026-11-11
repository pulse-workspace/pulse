-- 0001_init.up.sql
-- Unified production schema for Pulse (OSS Core + Enterprise Modules).
-- Generated for clean open-source release with durable sequence numbering.

-- =====================================================================
-- Pulse Schema Version Tracking
-- =====================================================================
CREATE TABLE IF NOT EXISTS pulse_schema_version (
    version    VARCHAR(64) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes      TEXT
);

-- =====================================================================
-- Users & Credentials
-- =====================================================================
CREATE TABLE IF NOT EXISTS users (
    user_id        VARCHAR(64) PRIMARY KEY,
    nickname       VARCHAR(255) NOT NULL,
    avatar         TEXT,
    email          VARCHAR(255) UNIQUE,
    phone          VARCHAR(32),
    status         VARCHAR(16) NOT NULL DEFAULT 'active',
    password_hash  VARCHAR(255) NOT NULL DEFAULT '',
    ext_data       JSONB,
    token_version  INT NOT NULL DEFAULT 1,
    mfa_required   BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_users_nickname   ON users(nickname);
CREATE INDEX IF NOT EXISTS idx_users_email      ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_status     ON users(status);
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at) WHERE deleted_at IS NOT NULL;

-- =====================================================================
-- Groups & Members
-- =====================================================================
CREATE TABLE IF NOT EXISTS groups (
    group_id     VARCHAR(64) PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    avatar       TEXT NOT NULL DEFAULT '',
    owner_id     VARCHAR(64) NOT NULL,
    group_type   VARCHAR(16) NOT NULL DEFAULT 'normal',
    member_count INT NOT NULL DEFAULT 0,
    max_members  INT NOT NULL DEFAULT 2000,
    introduction TEXT NOT NULL DEFAULT '',
    notification TEXT NOT NULL DEFAULT '',
    muted_until  TIMESTAMPTZ,
    settings     JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_groups_owner ON groups(owner_id);

CREATE TABLE IF NOT EXISTS group_members (
    group_id    VARCHAR(64) NOT NULL,
    user_id     VARCHAR(64) NOT NULL,
    nickname    TEXT NOT NULL DEFAULT '',
    role        VARCHAR(16) NOT NULL DEFAULT 'member',
    muted_until TIMESTAMPTZ,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON group_members(user_id);

-- =====================================================================
-- Conversations & Unread & Pins
-- =====================================================================
CREATE TABLE IF NOT EXISTS conversations (
    conv_id      VARCHAR(64) PRIMARY KEY,
    conv_type    SMALLINT NOT NULL DEFAULT 1,
    participants TEXT[] NOT NULL DEFAULT '{}',
    last_msg_id  VARCHAR(64),
    last_msg_at  TIMESTAMPTZ,
    pinned       BOOLEAN NOT NULL DEFAULT FALSE,
    max_seq      BIGINT NOT NULL DEFAULT 0,
    deleted_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conv_participants        ON conversations USING GIN(participants);
CREATE INDEX IF NOT EXISTS idx_conversations_pinned     ON conversations(pinned, last_msg_at DESC);
CREATE INDEX IF NOT EXISTS idx_conversations_deleted_at ON conversations(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS conversation_unread (
    conv_id          VARCHAR(64) NOT NULL,
    user_id          VARCHAR(64) NOT NULL,
    count            INT NOT NULL DEFAULT 0,
    last_read_msg_id VARCHAR(64),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conv_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_conv_unread_user ON conversation_unread(user_id);

CREATE TABLE IF NOT EXISTS conversation_pins (
    user_id    VARCHAR(64) NOT NULL,
    conv_id    VARCHAR(64) NOT NULL,
    pinned_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, conv_id)
);

-- =====================================================================
-- Messages & Full-Text Search
-- =====================================================================
CREATE TABLE IF NOT EXISTS messages (
    msg_id             VARCHAR(64) PRIMARY KEY,
    conv_id            VARCHAR(64) NOT NULL,
    sender_id          VARCHAR(64) NOT NULL,
    content_type       SMALLINT NOT NULL,
    content            JSONB NOT NULL,
    parent_msg_id      VARCHAR(64),
    mentioned_user_ids TEXT[] NOT NULL DEFAULT '{}',
    seq                BIGINT NOT NULL DEFAULT 0,
    tsv                tsvector GENERATED ALWAYS AS (
                           to_tsvector(
                               'simple',
                               coalesce(content->>'text', '') || ' ' ||
                               coalesce(content->>'filename', '') || ' ' ||
                               coalesce(content->>'url', '')
                           )
                       ) STORED,
    deleted_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at         TIMESTAMPTZ,
    edited_at          TIMESTAMPTZ,
    CONSTRAINT uq_messages_conv_seq UNIQUE (conv_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_messages_conv_created ON messages(conv_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_conv_seq     ON messages(conv_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_messages_sender       ON messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_deleted_at   ON messages(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_tsv          ON messages USING GIN(tsv);

-- =====================================================================
-- Reactions
-- =====================================================================
CREATE TABLE IF NOT EXISTS reactions (
    msg_id        VARCHAR(64) NOT NULL,
    user_id       VARCHAR(64) NOT NULL,
    reaction_type VARCHAR(32) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (msg_id, user_id, reaction_type)
);

-- =====================================================================
-- Friendships & Blacklists
-- =====================================================================
CREATE TABLE IF NOT EXISTS friendships (
    user_id     VARCHAR(64) NOT NULL,
    friend_id   VARCHAR(64) NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'accepted'
                CHECK (status IN ('accepted', 'pending_out', 'pending_in', 'blocked')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, friend_id)
);
CREATE INDEX IF NOT EXISTS idx_friendships_user   ON friendships(user_id);
CREATE INDEX IF NOT EXISTS idx_friendships_friend ON friendships(friend_id);
CREATE INDEX IF NOT EXISTS idx_friendships_status ON friendships(user_id, status);

CREATE TABLE IF NOT EXISTS blacklists (
    user_id     VARCHAR(64) NOT NULL,
    blocked_id  VARCHAR(64) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, blocked_id)
);

-- =====================================================================
-- User Sessions (Active Gateway Connections)
-- =====================================================================
CREATE TABLE IF NOT EXISTS user_sessions (
    session_id     VARCHAR(64) PRIMARY KEY,
    user_id        VARCHAR(64) NOT NULL,
    cell_id        VARCHAR(64) NOT NULL,
    platform       VARCHAR(16) NOT NULL,
    device_id      VARCHAR(128),
    ip             INET,
    user_agent     TEXT,
    connected_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user   ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_active ON user_sessions(last_active_at DESC);

-- =====================================================================
-- Event Sourcing Event Log & Outbox
-- =====================================================================
CREATE TABLE IF NOT EXISTS events (
    event_id    VARCHAR(64) PRIMARY KEY,
    stream      VARCHAR(128) NOT NULL,
    type        VARCHAR(64) NOT NULL,
    version     BIGINT NOT NULL,
    actor       VARCHAR(64),
    data        JSONB NOT NULL,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (stream, version)
);
CREATE INDEX IF NOT EXISTS idx_events_stream_version ON events(stream, version DESC);
CREATE INDEX IF NOT EXISTS idx_events_type           ON events(type);

CREATE TABLE IF NOT EXISTS projection_offsets (
    projection_name VARCHAR(128) PRIMARY KEY,
    last_event_id   VARCHAR(64),
    last_version    BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS outbox (
    outbox_id   BIGSERIAL PRIMARY KEY,
    target_cell VARCHAR(64) NOT NULL,
    subject     VARCHAR(255) NOT NULL,
    payload     JSONB NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts    INT NOT NULL DEFAULT 0,
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox(status, created_at) WHERE status = 'pending';

-- =====================================================================
-- Media Attachments
-- =====================================================================
CREATE TABLE IF NOT EXISTS media_attachments (
    attachment_id TEXT PRIMARY KEY,
    uploader_id   TEXT NOT NULL,
    conv_id       TEXT,
    object_key    TEXT NOT NULL UNIQUE,
    filename      TEXT NOT NULL,
    mime_type     TEXT NOT NULL,
    size_bytes    BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256_hex    CHAR(64),
    status        TEXT NOT NULL DEFAULT 'ready'
                  CHECK (status IN ('ready', 'deleted')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_media_uploader_created ON media_attachments(uploader_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_conv             ON media_attachments(conv_id);

-- =====================================================================
-- License Gate & System Admins
-- =====================================================================
CREATE TABLE IF NOT EXISTS admins (
    admin_id      VARCHAR(64) PRIMARY KEY,
    password_hash VARCHAR(255) NOT NULL,
    role          VARCHAR(32) NOT NULL DEFAULT 'admin',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS license_cache (
    cache_key   VARCHAR(128) PRIMARY KEY,
    payload     JSONB NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =====================================================================
-- SSO / SCIM / IdP
-- =====================================================================
CREATE TABLE IF NOT EXISTS idp_providers (
    idp_id                     VARCHAR(64)  PRIMARY KEY,
    name                       VARCHAR(255) NOT NULL,
    type                       VARCHAR(16)  NOT NULL,   -- 'saml' | 'oidc'
    enabled                    BOOLEAN      NOT NULL DEFAULT TRUE,
    config                     JSONB        NOT NULL,
    domain_restriction         TEXT[],
    allowed_redirect_uris      TEXT[]       DEFAULT '{}',
    saml_metadata_xml          TEXT,
    saml_entity_id             TEXT,
    saml_signing_key_path      TEXT,
    saml_want_assertions_signed BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at                 TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sso_links (
    user_id        VARCHAR(64)  PRIMARY KEY,
    idp_id         VARCHAR(64)  NOT NULL REFERENCES idp_providers(idp_id) ON DELETE CASCADE,
    external_id    VARCHAR(255) NOT NULL,
    external_email VARCHAR(255) NOT NULL,
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sso_links_idp ON sso_links(idp_id, external_id);

CREATE TABLE IF NOT EXISTS scim_tokens (
    token_id     VARCHAR(64)  PRIMARY KEY,
    idp_id       VARCHAR(64)  NOT NULL REFERENCES idp_providers(idp_id) ON DELETE CASCADE,
    token_hash   VARCHAR(255) NOT NULL,
    description  VARCHAR(255) NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_scim_tokens_idp ON scim_tokens(idp_id);

CREATE TABLE IF NOT EXISTS scim_groups (
    scim_id        VARCHAR(64)  PRIMARY KEY,
    pulse_group_id VARCHAR(64)  NOT NULL REFERENCES groups(group_id) ON DELETE CASCADE,
    display_name   TEXT         NOT NULL,
    external_id    TEXT,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS idp_group_mappings (
    idp_id         VARCHAR(64)  NOT NULL REFERENCES idp_providers(idp_id) ON DELETE CASCADE,
    external_group VARCHAR(255) NOT NULL,
    pulse_role     VARCHAR(32)  NOT NULL,
    pulse_group_id VARCHAR(64),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (idp_id, external_group)
);

-- =====================================================================
-- Compliance (Legal Hold, Retention, GDPR DSR) & RBAC
-- =====================================================================
CREATE TABLE IF NOT EXISTS legal_holds (
    hold_id     VARCHAR(64) PRIMARY KEY,
    scope_type  VARCHAR(16) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    reason      TEXT        NOT NULL,
    placed_by   VARCHAR(64) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    released_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_legal_holds_scope ON legal_holds(scope_type, scope_id) WHERE released_at IS NULL;

CREATE TABLE IF NOT EXISTS retention_policies (
    policy_id   VARCHAR(64)  PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    scope_type  VARCHAR(16)  NOT NULL,
    scope_id    VARCHAR(64),
    retain_days INT          NOT NULL,
    enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dsr_requests (
    request_id                      VARCHAR(64) PRIMARY KEY,
    user_id                         VARCHAR(64) NOT NULL,
    type                            VARCHAR(16) NOT NULL,
    status                          VARCHAR(16) NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending','running','completed','failed','in_progress','done','exported','deleted')),
    placed_by                       TEXT        NOT NULL DEFAULT 'system',
    processed_by                    TEXT,
    processed_at                    TIMESTAMPTZ,
    requested_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at                      TIMESTAMPTZ,
    completed_at                    TIMESTAMPTZ,
    artifact_path                   TEXT,
    artifact_bucket                 TEXT        DEFAULT 'pulse-dsr-exports',
    artifact_key                    TEXT,
    artifact_signed_url_expires_at  TIMESTAMPTZ,
    soft_deleted_rows               INT         NOT NULL DEFAULT 0,
    held_items_blocked              INT         NOT NULL DEFAULT 0,
    error_message                   TEXT
);
CREATE INDEX IF NOT EXISTS idx_dsr_user ON dsr_requests(user_id);

CREATE TABLE IF NOT EXISTS roles (
    role_id     VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(64) NOT NULL UNIQUE,
    description TEXT,
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id    VARCHAR(64) NOT NULL REFERENCES roles(role_id) ON DELETE CASCADE,
    permission VARCHAR(64) NOT NULL,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id    VARCHAR(64) NOT NULL,
    role_id    VARCHAR(64) NOT NULL REFERENCES roles(role_id) ON DELETE CASCADE,
    scope_type VARCHAR(16) NOT NULL DEFAULT 'workspace',
    scope_id   VARCHAR(64),
    granted_by VARCHAR(64) NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id, scope_type, scope_id)
);
CREATE INDEX IF NOT EXISTS idx_user_roles_user ON user_roles(user_id);

CREATE TABLE IF NOT EXISTS org_units (
    unit_id   VARCHAR(64)  PRIMARY KEY,
    parent_id VARCHAR(64),
    name      VARCHAR(255) NOT NULL,
    type      VARCHAR(16)  NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_org_units_parent ON org_units(parent_id);

CREATE TABLE IF NOT EXISTS user_org_memberships (
    user_id   VARCHAR(64) NOT NULL,
    unit_id   VARCHAR(64) NOT NULL REFERENCES org_units(unit_id) ON DELETE CASCADE,
    role      VARCHAR(16) NOT NULL DEFAULT 'member',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, unit_id)
);

-- =====================================================================
-- MFA Credentials (WebAuthn / Passkeys)
-- =====================================================================
CREATE TABLE IF NOT EXISTS mfa_credentials (
    credential_id TEXT        PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    relying_party TEXT        NOT NULL,
    credential    JSONB       NOT NULL,
    name          TEXT        NOT NULL,
    aaguid        UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ,
    CONSTRAINT fk_mfa_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_mfa_credentials_user ON mfa_credentials(user_id);

-- =====================================================================
-- Backups & Restore Auditing
-- =====================================================================
CREATE TABLE IF NOT EXISTS backup_runs (
    id             UUID PRIMARY KEY,
    kind           TEXT NOT NULL CHECK (kind IN ('pitr', 'nats_snapshot')),
    started_at     TIMESTAMPTZ NOT NULL,
    completed_at   TIMESTAMPTZ,
    status         TEXT NOT NULL CHECK (status IN ('running', 'ok', 'fail')),
    s3_uri         TEXT,
    error_message  TEXT,
    bytes_uploaded BIGINT DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_backup_runs_started_at  ON backup_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_backup_runs_kind_status ON backup_runs(kind, status);

CREATE TABLE IF NOT EXISTS backup_audit (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    action        TEXT        NOT NULL CHECK (action IN ('backup.restore')),
    actor         TEXT        NOT NULL,
    snapshot_path TEXT,
    rows_restored INT         NOT NULL DEFAULT 0,
    status        TEXT        NOT NULL CHECK (status IN ('ok', 'fail')),
    reason        TEXT,
    error_message TEXT,
    started_at    TIMESTAMPTZ NOT NULL,
    finished_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_backup_audit_started_at    ON backup_audit(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_backup_audit_action_status ON backup_audit(action, status);
CREATE INDEX IF NOT EXISTS idx_backup_audit_status_reason ON backup_audit(status, reason);

-- =====================================================================
-- Whitelabeling & Email Templates
-- =====================================================================
CREATE TABLE IF NOT EXISTS whitelabel_config (
    id                  INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    brand_name          TEXT NOT NULL DEFAULT 'Pulse',
    brand_logo_url      TEXT NOT NULL DEFAULT '',
    brand_color_primary TEXT NOT NULL DEFAULT '#3B82F6',
    support_url         TEXT NOT NULL DEFAULT '',
    template_dir        TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by          TEXT NOT NULL DEFAULT 'system'
);
INSERT INTO whitelabel_config (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS whitelabel_templates (
    tenant_id  TEXT        NOT NULL DEFAULT 'default',
    name       TEXT        NOT NULL,
    subject    TEXT        NOT NULL DEFAULT '',
    html_body  TEXT        NOT NULL DEFAULT '',
    text_body  TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by TEXT        NOT NULL DEFAULT 'system',
    PRIMARY KEY (tenant_id, name)
);
INSERT INTO whitelabel_templates (tenant_id, name) VALUES
    ('default', 'welcome'),
    ('default', 'password_reset'),
    ('default', 'invite')
ON CONFLICT (tenant_id, name) DO NOTHING;

-- =====================================================================
-- Bridges (Telegram, Slack, Discord)
-- =====================================================================
CREATE TABLE IF NOT EXISTS bridges (
    bridge_id       TEXT PRIMARY KEY,
    platform        TEXT NOT NULL CHECK (platform IN ('telegram')),
    name            TEXT NOT NULL,
    bot_token       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'disabled' CHECK (status IN ('enabled', 'disabled')),
    default_chat_id TEXT,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_bridges_status ON bridges(status) WHERE status = 'enabled';

CREATE TABLE IF NOT EXISTS bridge_chat_mappings (
    bridge_id        TEXT NOT NULL,
    external_chat_id TEXT NOT NULL,
    conv_id          TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bridge_id, external_chat_id)
);
CREATE INDEX IF NOT EXISTS idx_bridge_chat_mappings_conv ON bridge_chat_mappings(conv_id);

CREATE OR REPLACE FUNCTION bridges_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_bridges_set_updated_at ON bridges;
CREATE TRIGGER trg_bridges_set_updated_at
    BEFORE UPDATE ON bridges
    FOR EACH ROW
    EXECUTE FUNCTION bridges_set_updated_at();

-- =====================================================================
-- SaaS Multi-Tenancy, API Keys & Metering
-- =====================================================================
CREATE TABLE IF NOT EXISTS tenants (
    tenant_id         VARCHAR(64) PRIMARY KEY,
    org_id            VARCHAR(64) NOT NULL,
    name              VARCHAR(255) NOT NULL,
    slug              VARCHAR(64) UNIQUE NOT NULL,
    tier              VARCHAR(32) NOT NULL DEFAULT 'free'
                      CHECK (tier IN ('free', 'starter', 'scale', 'enterprise')),
    status            VARCHAR(32) NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'suspended', 'quota_exceeded')),
    max_connections   INT NOT NULL DEFAULT 200,
    max_mau           INT NOT NULL DEFAULT 1000,
    max_storage_bytes BIGINT NOT NULL DEFAULT 5368709120, -- 5GB
    settings          JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tenants_org    ON tenants(org_id);
CREATE INDEX IF NOT EXISTS idx_tenants_slug   ON tenants(slug);
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

CREATE TABLE IF NOT EXISTS tenant_api_keys (
    key_id          VARCHAR(64) PRIMARY KEY,
    tenant_id       VARCHAR(64) NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    key_prefix      VARCHAR(16) NOT NULL,
    key_hash        VARCHAR(128) NOT NULL UNIQUE,
    key_type        VARCHAR(16) NOT NULL CHECK (key_type IN ('publishable', 'secret')),
    scopes          TEXT[] NOT NULL DEFAULT '{}',
    allowed_origins TEXT[],
    last_used_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_tenant ON tenant_api_keys(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_hash   ON tenant_api_keys(key_hash);

CREATE TABLE IF NOT EXISTS tenant_usage_daily (
    tenant_id        VARCHAR(64) NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    usage_date       DATE NOT NULL,
    active_users     INT NOT NULL DEFAULT 0,
    message_count    BIGINT NOT NULL DEFAULT 0,
    peak_connections INT NOT NULL DEFAULT 0,
    storage_bytes    BIGINT NOT NULL DEFAULT 0,
    egress_bytes     BIGINT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, usage_date)
);
CREATE INDEX IF NOT EXISTS idx_tenant_usage_date ON tenant_usage_daily(usage_date);

CREATE TABLE IF NOT EXISTS tenant_webhooks (
    webhook_id VARCHAR(64) PRIMARY KEY,
    tenant_id  VARCHAR(64) NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    target_url TEXT NOT NULL,
    secret     VARCHAR(128) NOT NULL,
    events     TEXT[] NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tenant_webhooks_tenant ON tenant_webhooks(tenant_id);

INSERT INTO tenants (tenant_id, org_id, name, slug, tier, status, max_connections, max_mau)
VALUES ('default', 'org_default', 'Default Organization', 'default', 'enterprise', 'active', 50000, 500000)
ON CONFLICT (tenant_id) DO NOTHING;

-- Seed version record
INSERT INTO pulse_schema_version (version, notes)
VALUES ('0001_init', 'Pulse consolidated production schema (v1.0.0)')
ON CONFLICT DO NOTHING;
