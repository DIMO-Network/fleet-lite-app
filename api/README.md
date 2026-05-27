# api/

Placeholder. The Go backend has not been implemented yet — fleet-lite-app
currently ships only the `web/` frontend.

When it lands, expect it to mirror `rental-fleets-app/api/`:

- `cmd/fleet-lite-app/` — CLI entry point with `migrate` subcommand
- `internal/` — `app`, `config`, `controllers`, `core`, `db`, `gateway`,
  `models`, `service`, `test`
- `settings.sample.yaml` checked in; real `settings.yaml` gitignored
- goose for migrations, sqlboiler for ORM, testcontainers for integration
  tests
- HTTPS in dev via `USE_DEV_CERTS: true`, reading certs from `../web/.mkcert/`

See [../AGENTS.md](../AGENTS.md) for the conventions the Go code will follow.
