-- +goose Up

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    first_name    text        NOT NULL,
    last_name     text        NOT NULL,
    email         text        NOT NULL UNIQUE,
    phone         text        NOT NULL UNIQUE,
    city          text        NOT NULL,
    language      text        NOT NULL CHECK (language IN ('tr', 'en')),
    birth_date    date        NOT NULL,
    loyalty_tier  text        NOT NULL CHECK (loyalty_tier IN ('bronze', 'silver', 'gold', 'platinum')),
    sms_opt_in    boolean     NOT NULL,
    email_opt_in  boolean     NOT NULL,
    total_orders  integer     NOT NULL DEFAULT 0,
    total_spent   numeric(12, 2) NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX users_city_idx ON users (city);

CREATE TABLE events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type        text        NOT NULL CHECK (type IN ('page_view', 'product_view', 'cart_add', 'purchase')),
    category    text        NOT NULL CHECK (category IN ('electronics', 'fashion', 'home', 'beauty', 'sports')),
    amount      numeric(10, 2),
    occurred_at timestamptz NOT NULL
);

-- Segment predicates are "user has/has not an event of type T (in category C) since X":
-- (user_id, type, occurred_at) serves the correlated EXISTS lookups.
CREATE INDEX events_user_type_time_idx ON events (user_id, type, occurred_at);

CREATE TABLE campaigns (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    brief         text        NOT NULL CHECK (char_length(brief) BETWEEN 10 AND 2000),
    status        text        NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft', 'scheduled', 'sending', 'completed', 'cancelled')),
    segment       jsonb,  -- validated segment.Rule
    segment_notes jsonb,  -- Claude's summary / assumptions / unsupported parts of the brief
    audience      jsonb,  -- audience counts at preview time
    content       jsonb,  -- validated content.Content
    channels      text[]      NOT NULL DEFAULT '{}' CHECK (channels <@ ARRAY['sms', 'email']),
    scheduled_at  timestamptz,
    approved_at   timestamptz,
    started_at    timestamptz,
    completed_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX campaigns_scheduled_idx ON campaigns (scheduled_at) WHERE status = 'scheduled';

-- One row per (campaign, user, channel): the UNIQUE constraint is the first
-- line of the "never send the same message twice" guarantee.
CREATE TABLE deliveries (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id         bigint      NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    user_id             bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel             text        NOT NULL CHECK (channel IN ('sms', 'email')),
    status              text        NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'sending', 'retrying', 'sent', 'failed', 'skipped', 'cancelled')),
    attempts            integer     NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz,
    last_error          text,
    provider_message_id text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz,
    UNIQUE (campaign_id, user_id, channel),
    CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);

CREATE INDEX deliveries_campaign_status_idx ON deliveries (campaign_id, channel, status);
CREATE INDEX deliveries_unfinished_idx ON deliveries (status, updated_at)
    WHERE status IN ('queued', 'sending', 'retrying');

-- Audit trail of every LLM call: what was asked, what came back, and why it was rejected.
CREATE TABLE llm_calls (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id    bigint      REFERENCES campaigns (id) ON DELETE CASCADE,
    purpose        text        NOT NULL CHECK (purpose IN ('segment', 'content')),
    attempt        integer     NOT NULL,
    provider       text        NOT NULL,
    model          text        NOT NULL,
    prompt_version text        NOT NULL,
    outcome        text        NOT NULL CHECK (outcome IN ('ok', 'invalid', 'error')),
    errors         jsonb,
    raw_output     text,
    stop_reason    text,
    input_tokens   integer,
    output_tokens  integer,
    latency_ms     integer     NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX llm_calls_campaign_idx ON llm_calls (campaign_id, created_at);

-- +goose Down
DROP TABLE llm_calls;
DROP TABLE deliveries;
DROP TABLE campaigns;
DROP TABLE events;
DROP TABLE users;
