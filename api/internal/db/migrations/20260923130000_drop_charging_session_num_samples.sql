-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- num_samples counted raw IsCharging telemetry samples for the hand-rolled
-- sweep detector (charging_detection.go). ChargingDetectionService now
-- sources sessions from telemetry-api's native `recharge` segmentation
-- instead, which doesn't expose a sample count, so the column no longer has
-- a value to hold.
ALTER TABLE charging_sessions DROP COLUMN IF EXISTS num_samples;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE charging_sessions ADD COLUMN IF NOT EXISTS num_samples INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd
