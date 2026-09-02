# Contributing to GitLab Fleet Governor

Thank you for your interest in contributing to **GitLab Fleet Governor**! We welcome contributions, bug reports, feature requests, and documentation improvements.

---

## Development Prerequisites

- **Go**: Version 1.26 or higher.
- **Git**: Modern version.
- **Make**: Standard build automation tool.
- **golangci-lint**: Optional for local linting (`v1.60+`).
- **Docker**: Optional for container build verification.

---

## Local Development Workflow

### 1. Clone & Build

```bash
git clone https://github.com/divmora/gitlab-fleet-governor.git
cd gitlab-fleet-governor

# Compile binary into bin/gitlab-fleet-governor
make build
```

### 2. Running Unit & Integration Tests

All tests leverage in-memory mock GitLab servers without needing a live network connection:

```bash
# Run all unit and integration tests with race detector and coverage
make test

# View test coverage profile
make cover
```

### 3. Code Formatting & Linting

```bash
# Verify formatting and static analysis
make lint

# Run go vet
go vet ./...
```

---

## Available Make Targets

| Target | Description |
|---|---|
| `make build` | Compile host binary into `bin/gitlab-fleet-governor` |
| `make test` | Run test suite with race detector and coverage |
| `make test-coverage` / `make cover` | Generate HTML code coverage report |
| `make fmt` | Format Go source code (`gofmt` and `goimports`) |
| `make lint` | Run `golangci-lint` static analysis |
| `make dev-setup` | Tidy and verify Go module dependencies |
| `make build-lambda` / `make lambda-package` | Build AWS Lambda custom runtime bootstrap bundle |
| `make docker-build` | Build local Docker image |
| `make clean` | Remove binaries, dist bundles, and test coverage files |

---

## Conventional Commits

We adhere to the [Conventional Commits](https://www.conventionalcommits.org/) specification for automated release management via Release Please:

```
<type>(<scope>): <short summary>

[optional body]

[optional footer(s)]
```

### Common Types:
- `feat`: New user-facing feature or reconciler capability.
- `fix`: Bug fix in CLI, reconcilers, or client resilience.
- `docs`: Documentation site and example updates.
- `chore`: Dependency bumps, build adjustments, or internal refactoring.
- `test`: Adding or refactoring test suites.
- `ci`: Changes to GitHub Actions workflows or GoReleaser.

---

## Pull Request Guidelines

1. **Branch Naming**: Use descriptive branch names like `feat/webhook-pruning` or `fix/push-rule-404`.
2. **Test Coverage**: Ensure all new features or bug fixes are accompanied by unit/mock integration tests.
3. **Clean Build**: Ensure `go test -v -race -cover ./...` passes with 0 errors.
4. **Documentation**: Update corresponding `docs/*.md` and `examples/*.yaml` when modifying schemas or behaviors.

---

## Licensing of Contributions

By submitting a pull request or contributing to this repository, you agree that your contributions will be licensed under the project's **Business Source License 1.1 (BSL 1.1)** (and the resulting Apache License 2.0 conversion terms upon each version's Change Date), as detailed in [LICENSE](LICENSE) and [DIVMORA Licensing Policy](https://github.com/divmora/.github/blob/main/LICENSING.md).

