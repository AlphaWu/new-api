# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

@AGENTS.md

## Commands

### Backend (Go, repository root)

- Build: `go build .`
- Run dev server: `go run main.go` (serves the API and the embedded frontend on `:3000`)
- Run all backend tests (root module + relaykit): `make test`
- Run root-module tests only: `GOWORK=off go test ./...`
- Run a single package or test: `go test ./relay/channel/openai -run TestImageRequest`
- Vet: `go vet ./...`

### relaykit (nested Go module)

`relaykit/` is a separate Go module wired into the root module via a `replace` directive
(`replace github.com/QuantumNous/new-api/relaykit => ./relaykit`), **not** a go.work workspace.
Always build/test it independently with `GOWORK=off`:

- Build: `cd relaykit && GOWORK=off go build ./...`
- Test: `cd relaykit && GOWORK=off go test ./...`

### Frontend (`web/`, Bun)

- Install dependencies: `cd web && bun install`
- Dev server: `cd web && bun run dev`
- Build: `cd web && bun run build`
- Typecheck: `cd web && bun run typecheck` (runs `tsgo -b`)
- Lint: `cd web && bun run lint` (oxlint; `bun run lint:fix` to autofix)
- Test: `cd web && bun run test` (Vitest; `bun run test:watch` for watch mode)
- i18n key sync: `cd web && bun run i18n:sync`

### Make targets

`make all`, `build-web`, `start-api`, `dev` (docker API + web dev), `dev-api`, `dev-api-rebuild`,
`dev-web`, `test`, `reset-setup` (resets the local setup-wizard state and root users).

## Build-order note

The Go binary embeds `web/dist`; run `make build-web` (or `cd web && bun run build`) before
`go build .` so the compiled frontend is served.
