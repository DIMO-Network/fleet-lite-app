-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- charging_sessions is the cached, summary-form result of EV charging-session
-- detection: one row per contiguous interval a vehicle's traction battery
-- reported isCharging=true, computed on-demand from telemetry and cached
-- because the past is immutable. Energy/power/SOC/location fields are
-- nullable — an interval that didn't report a given signal just omits it
-- rather than reporting a false 0. See
-- docs/superpowers/specs/2026-09-14-charging-reporting-design.md.
CREATE TABLE IF NOT EXISTS charging_sessions (
    tenant_id        UUID NOT NULL,
    token_id         BIGINT NOT NULL,
    started_at       TIMESTAMPTZ NOT NULL,
    ended_at         TIMESTAMPTZ NOT NULL,
    added_energy_kwh DOUBLE PRECISION,
    avg_power_kw     DOUBLE PRECISION,
    soc_start_pct    DOUBLE PRECISION,
    soc_end_pct      DOUBLE PRECISION,
    lat              DOUBLE PRECISION,
    lng              DOUBLE PRECISION,
    num_samples      INTEGER NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, started_at)
);

CREATE INDEX IF NOT EXISTS idx_charging_sessions_token ON charging_sessions (tenant_id, token_id, started_at);

-- charging_scan_coverage records which (vehicle, time-range) windows have
-- already been analyzed, independent of whether any session was found —
-- mirrors geofence_scan_coverage, minus the per-geofence dimension (charging
-- detection isn't scoped to a drawn shape). Before computing, the requested
-- window is checked against existing coverage so a repeat request for the
-- same range never re-fetches telemetry.
CREATE TABLE IF NOT EXISTS charging_scan_coverage (
    tenant_id    UUID   NOT NULL,
    token_id     BIGINT NOT NULL,
    scanned_from TIMESTAMPTZ NOT NULL,
    scanned_to   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, scanned_from)
);

-- tenant_charging_settings holds one tenant's electricity-rate and gas-price
-- assumptions used to price charging sessions and estimate $ saved vs.
-- gasoline. Optional — no row means the Charging tab still shows kWh totals
-- but leaves cost/savings blank.
CREATE TABLE IF NOT EXISTS tenant_charging_settings (
    tenant_id            UUID PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    electricity_rate     NUMERIC,
    gas_price            NUMERIC,
    gas_mpg_equivalent   NUMERIC,
    vehicle_kwh_per_mile NUMERIC,
    currency             TEXT NOT NULL DEFAULT 'USD',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS tenant_charging_settings;
DROP TABLE IF EXISTS charging_scan_coverage;
DROP TABLE IF EXISTS charging_sessions;

-- +goose StatementEnd
