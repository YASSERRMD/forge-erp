# Backend Bounded-Context Pattern

Every context `backend/internal/<ctx>/` ships four files (split further only
when a file exceeds ~400 lines):

- `domain.go` — entities, value objects, status machines, validation.
  No imports from other contexts or from pgx. Pure Go + stdlib (+ platform types).
- `repository.go` — `Repository` interface + `PostgresRepository` (pgx) +
  `MemoryRepository` (tests/dev). Interface lives with the service's needs,
  not the database's shape.
- `service.go` — business rules, transaction boundaries (`platform.Tx`),
  publishes domain events via `platform.Bus`. Depends on interfaces only.
- `handler.go` — chi handlers: decode → validate → service → encode.
  Auth via `platform` middleware; permission strings `<ctx>.<action>`.
  All routes registered in `RegisterRoutes(r chi.Router, ...)`.

Plus per context:

- `backend/migrations/NNNN_<ctx>.up.sql` (+ `.down.sql` for manual recovery).
- OpenAPI paths appended to `api/openapi.yaml` (source of truth for HTTP).
- Tests: table-driven domain/service tests + `httptest` handler tests;
  repository tests run against `MemoryRepository` by default and against real
  PostgreSQL when `TEST_DATABASE_URL` is set (skip otherwise, never fail).
