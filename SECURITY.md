# Security Policy

## Supported Versions

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ |

Only the latest release receives security updates. Please upgrade to the latest version before reporting issues.

## Reporting a Vulnerability

**Please do NOT report security vulnerabilities through public GitHub issues.**

Instead, please report them via [GitHub Security Advisories](https://github.com/vmvarela/ghoten/security/advisories/new).

You should receive a response within **72 hours**. If for some reason you do not, please follow up to ensure the report was received.

### What to Include

- Type of vulnerability (e.g., remote code execution, state data leak, authentication bypass).
- Full paths of source file(s) related to the issue.
- Location of the affected source code (tag/branch/commit or direct URL).
- Step-by-step instructions to reproduce the issue.
- Proof-of-concept or exploit code (if available).
- Impact of the issue, including how an attacker might exploit it.

### What to Expect

1. **Acknowledgement** — within 72 hours.
2. **Assessment** — we will evaluate severity and impact.
3. **Fix** — a patch will be developed and tested.
4. **Disclosure** — a security advisory will be published alongside the fix release.

We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure). Please give us reasonable time to address the vulnerability before public disclosure.

## Security Best Practices for Users

When using Ghoten with the ORAS backend:

- **Enable [state encryption](https://opentofu.org/docs/language/state/encryption/)** — encrypts state before it leaves your machine.
- Keep OCI repositories **private** with **least-privilege** access tokens.
- Use registries that provide **encryption at rest** and **audit trails**.
- Rotate credentials regularly.
- Be mindful of state version retention — longer history increases exposure surface.

## Scope

This security policy covers:

- The Ghoten binary and its source code.
- The ORAS backend implementation.
- Official Docker images published to `ghcr.io/vmvarela/ghoten`.

It does **not** cover:

- Third-party providers or modules.
- OCI registries themselves (report issues to the registry provider).
- Upstream OpenTofu vulnerabilities (report to [OpenTofu security](https://github.com/opentofu/opentofu/security)).
