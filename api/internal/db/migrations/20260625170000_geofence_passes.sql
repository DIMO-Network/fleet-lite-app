-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- geofence_passes is the cached, summary-form result of geofence event
-- detection (Phase 2; see docs/GEOFENCES_PLAN.md). One row = one "pass": a
-- contiguous interval a vehicle (token_id) was inside a geofence's polygon,
-- computed on-demand from telemetry and cached because the past is immutable.
-- We store raw max_speed_kph (not a speed-exceeded verdict): the geofence's
-- speed_limit_kph can change on edit, so "exceeded?" is evaluated against the
-- current limit at read time. Geometry is immutable per geofence id (edit =
-- delete+redraw), so a cached pass never goes stale. Rows cascade when the
-- geofence is deleted.
CREATE TABLE IF NOT EXISTS geofence_passes (
    geofence_id   TEXT   NOT NULL REFERENCES geofences (id) ON DELETE CASCADE,
    tenant_id     UUID   NOT NULL,
    token_id      BIGINT NOT NULL,
    entered_at    TIMESTAMPTZ NOT NULL,
    exited_at     TIMESTAMPTZ NOT NULL,
    dwell_s       INTEGER NOT NULL,
    max_speed_kph DOUBLE PRECISION,            -- nullable: speed may be absent in the window
    entry_lat     DOUBLE PRECISION NOT NULL,
    entry_lng     DOUBLE PRECISION NOT NULL,
    exit_lat      DOUBLE PRECISION NOT NULL,
    exit_lng      DOUBLE PRECISION NOT NULL,
    max_speed_lat DOUBLE PRECISION,            -- coords of the fastest sample in the pass
    max_speed_lng DOUBLE PRECISION,
    num_samples   INTEGER NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (geofence_id, token_id, entered_at)
);

CREATE INDEX IF NOT EXISTS idx_geofence_passes_token ON geofence_passes (tenant_id, token_id, entered_at);
CREATE INDEX IF NOT EXISTS idx_geofence_passes_geofence_window ON geofence_passes (geofence_id, entered_at);

-- geofence_scan_coverage records which (geofence, vehicle, time-range) windows
-- have already been analyzed, independent of whether any pass was found. Before
-- computing, we subtract covered ranges from the requested window and only fetch
-- telemetry for the gaps; a window with zero passes is still recorded so it is
-- never recomputed. Rows cascade when the geofence is deleted.
CREATE TABLE IF NOT EXISTS geofence_scan_coverage (
    geofence_id  TEXT   NOT NULL REFERENCES geofences (id) ON DELETE CASCADE,
    tenant_id    UUID   NOT NULL,
    token_id     BIGINT NOT NULL,
    scanned_from TIMESTAMPTZ NOT NULL,
    scanned_to   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (geofence_id, token_id, scanned_from)
);

CREATE INDEX IF NOT EXISTS idx_geofence_coverage_lookup ON geofence_scan_coverage (geofence_id, token_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS geofence_scan_coverage;
DROP TABLE IF EXISTS geofence_passes;

-- +goose StatementEnd
