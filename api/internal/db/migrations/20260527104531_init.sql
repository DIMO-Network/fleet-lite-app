-- +goose Up
-- +goose StatementBegin
-- No-op initial migration. The schema is created in the migrate subcommand
-- before goose runs; this file exists so `goose up` has at least one
-- migration to record, and so future migrations have a clear predecessor.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
