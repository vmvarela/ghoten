# Contributing to Ghoten

Thank you for your interest in contributing to Ghoten! This document provides guidelines and information to help you get started.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [How Can I Contribute?](#how-can-i-contribute)
- [Development Setup](#development-setup)
- [Pull Request Process](#pull-request-process)
- [Coding Standards](#coding-standards)
- [Reporting Security Issues](#reporting-security-issues)

## Code of Conduct

This project adheres to a [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. Please report unacceptable behaviour to the maintainers.

## How Can I Contribute?

### Reporting Bugs

- Check [existing issues](https://github.com/vmvarela/ghoten/issues) before creating a new one.
- Use the **Bug Report** issue template.
- Include steps to reproduce, expected vs. actual behaviour, and relevant logs (`TF_LOG=DEBUG ghoten plan`).

### Suggesting Features

- Open a **Feature Request** issue.
- Describe the problem it solves, proposed solution, and alternatives considered.

### Submitting Code

1. Fork the repository.
2. Create a feature branch from `master` (`git checkout -b feature/my-change`).
3. Make your changes with tests.
4. Submit a pull request targeting `master`.

## Development Setup

### Prerequisites

- **Go** — version specified in [go.mod](go.mod)
- **Make**
- **Docker** (optional, for image builds)

### Build

```bash
git clone https://github.com/vmvarela/ghoten.git
cd ghoten
make build
```

### Test

```bash
# Full test suite
make test

# With coverage
make test-with-coverage

# ORAS backend only
go test ./internal/backend/remote-state/oras/...
```

### Lint

```bash
make golangci-lint
```

## Pull Request Process

1. Ensure all tests pass (`make test`).
2. Ensure code passes linting (`make golangci-lint`).
3. Run `go mod tidy` if you changed dependencies.
4. Update documentation if you changed user-facing behaviour.
5. Fill out the pull request template completely.
6. A maintainer will review your PR. Address any requested changes.

### Commit Messages

- Use clear, descriptive commit messages.
- Prefer [Conventional Commits](https://www.conventionalcommits.org/) format:
  ```
  feat: add retry configuration for ORAS backend
  fix: correct lock TTL calculation
  docs: update installation instructions
  ```

## Coding Standards

- Follow the existing code style and conventions in the project.
- Use `gofmt` / `goimports` for formatting.
- All exported types and functions must have doc comments.
- Write unit tests for new functionality.
- Keep changes focused — one logical change per PR.

## Reporting Security Issues

**Do not open a public issue for security vulnerabilities.** Please see [SECURITY.md](SECURITY.md) for responsible disclosure instructions.

## License

By contributing, you agree that your contributions will be licensed under the [Mozilla Public License 2.0](LICENSE).
