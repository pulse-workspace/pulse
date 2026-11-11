# Contributing to Pulse

Thank you for your interest in contributing to Pulse! We welcome issues, documentation improvements, bug fixes, and feature contributions from the community.

---

## Code of Conduct

We are committed to providing a welcoming, inclusive, and harassment-free experience for everyone. Please be respectful and constructive in all communications.

---

## Development Setup

1. Fork and clone the repository.
2. Start dependencies using Docker:
   ```bash
   docker compose up -d postgres nats rustfs
   ```
3. Run schema migrations:
   ```bash
   make migrate-up
   ```
4. Verify unit tests:
   ```bash
   make test-unit
   ```

---

## Code Standards

- **Formatting**: Always format code using `go fmt` or `make fmt`.
- **Linting**: Ensure code passes `golangci-lint run ./...` (`make lint`).
- **Comments**:
  - Write clear Go doc comments on all exported functions, structs, and interfaces.
  - Comments must describe **what** the function does, business logic, inputs, and outputs.
  - Do not reference internal project phases, sprint tickets, or confidential milestones in comments.
- **Testing**:
  - Add unit tests for any new business logic in domain packages.
  - Tests requiring live infrastructure should use appropriate build tags (e.g. `//go:build integration` or `//go:build contract`).

---

## Submitting Pull Requests

1. Create a feature branch: `git checkout -b feature/your-feature-name`.
2. Commit changes with clear, semantic commit messages:
   - `feat: add presence broadcast filter`
   - `fix: resolve race condition in sequence counter`
   - `docs: update websocket protocol table`
3. Push to your fork and submit a Pull Request to `main`.

