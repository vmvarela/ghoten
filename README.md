# Ghoten

[![Release](https://img.shields.io/github/v/release/vmvarela/ghoten?label=Latest%20Release&style=flat-square)](https://github.com/vmvarela/ghoten/releases/latest)
[![OpenTofu Base](https://img.shields.io/badge/Based%20on-OpenTofu-blue?style=flat-square)](https://github.com/opentofu/opentofu)
[![License](https://img.shields.io/badge/License-MPL%202.0-brightgreen.svg?style=flat-square)](LICENSE)

**Ghoten** is a personal fork of [OpenTofu](https://github.com/opentofu/opentofu) that adds native support for storing Terraform/OpenTofu state in **OCI registries** like GitHub Container Registry (GHCR), Amazon ECR, Azure ACR, and others.

> 🎯 **Goal**: The ORAS backend developed here is intended to be contributed back to OpenTofu upstream.

---

## Why Ghoten?

Store your infrastructure state alongside your container images. No additional cloud storage accounts, no SaaS dependencies—just your existing OCI registry.

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/your-org/tf-state"
  }
}
```

---

## 🚀 Quick Start

```bash
# Install
curl -sSL https://raw.githubusercontent.com/vmvarela/ghoten/develop/install.sh | sh

# Authenticate (for GHCR)
gh auth login

# Use it
ghoten init
ghoten apply
```

> ℹ️ Ghoten installs as a separate binary and doesn't interfere with existing `tofu` or `terraform` installations.

---

## 📦 Features

| Feature | Description |
|---------|-------------|
| **OCI Registry Storage** | Store state as OCI artifacts in any compatible registry |
| **Supported Registries** | GHCR, Amazon ECR, Azure ACR, Google GCR, Docker Hub, Harbor |
| **Reuse Existing Auth** | Uses Docker credentials and registry login tokens |
| **Distributed Locking** | Best-effort locking to prevent concurrent modifications |
| **State Versioning** | Keep history of state versions with configurable retention |
| **Compression** | Optional gzip compression for state files |
| **Encryption Compatible** | Works with OpenTofu's client-side state encryption |

---

## ✅ When to Use Ghoten

- Individual operators or small teams
- CI/CD pipelines with existing OCI registry access
- Open source projects
- Environments where OCI registries are already available

## 🚫 When NOT to Use

- Large teams with heavy concurrent access
- Strong locking or compliance requirements
- Environments that mandate managed SaaS backends

---

## 🧰 Configuration Examples

### Minimal

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/your-org/tf-state"
  }
}
```

### Advanced (versioning + encryption)

```hcl
terraform {
  backend "oras" {
    repository  = "ghcr.io/your-org/tf-state"
    compression = "gzip"

    versioning {
      enabled      = true
      max_versions = 10
    }
  }

  encryption {
    key_provider "pbkdf2" "main" {
      passphrase = var.state_passphrase
    }
    method "aes_gcm" "main" {
      key_provider = key_provider.pbkdf2.main
    }
    state {
      method = method.aes_gcm.main
    }
  }
}
```

### 📚 Full Documentation

See the [ORAS Backend README](internal/backend/remote-state/oras/README.md) for:
- All configuration parameters
- Authentication setup
- Locking behavior
- Versioning and retention
- Troubleshooting

---

## 📥 Installation

### Linux/macOS

```bash
curl -sSL https://raw.githubusercontent.com/vmvarela/ghoten/develop/install.sh | sh
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/vmvarela/ghoten/develop/install.ps1 | iex
```

### Installation Options

| Variable | Description | Default |
|----------|-------------|---------|
| `GHOTEN_VERSION` | Specific version to install | Latest |
| `GHOTEN_INSTALL_DIR` | Installation directory | `/usr/local/bin` |
| `GHOTEN_BINARY_NAME` | Binary name | `ghoten` |

**Examples:**

```bash
# Install specific version
GHOTEN_VERSION=v1.12.0 curl -sSL https://raw.githubusercontent.com/vmvarela/ghoten/develop/install.sh | sh

# Install to custom directory
GHOTEN_INSTALL_DIR=~/.local/bin curl -sSL https://raw.githubusercontent.com/vmvarela/ghoten/develop/install.sh | sh
```

### Build from Source

```bash
git clone https://github.com/vmvarela/ghoten.git
cd ghoten
make build  # Creates ./ghoten binary
```

### Manual Download

Download binaries from the [Releases](https://github.com/vmvarela/ghoten/releases) page.

---

## 🔄 Versioning

Ghoten follows OpenTofu releases:

| OpenTofu | Ghoten |
|----------|--------|
| `v1.12.0` | `v1.12.0` |

The fork syncs with upstream OpenTofu to incorporate improvements and security fixes.

---

## 🧪 Project Status

Actively developed and usable. APIs and backend format may evolve based on feedback.

---

## 📜 About OpenTofu

<details>
<summary>Click to expand original OpenTofu information</summary>

# OpenTofu

- [HomePage](https://opentofu.org/)
- [How to install](https://opentofu.org/docs/intro/install)
- [Join the Slack community](https://opentofu.org/slack)

![](https://raw.githubusercontent.com/opentofu/brand-artifacts/main/full/transparent/SVG/on-dark.svg#gh-dark-mode-only)
![](https://raw.githubusercontent.com/opentofu/brand-artifacts/main/full/transparent/SVG/on-light.svg#gh-light-mode-only)

[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/10508/badge)](https://www.bestpractices.dev/projects/10508)

OpenTofu is an OSS tool for building, changing, and versioning infrastructure safely and efficiently. OpenTofu can manage existing and popular service providers as well as custom in-house solutions.

The key features of OpenTofu are:

- **Infrastructure as Code**: Infrastructure is described using a high-level configuration syntax.
- **Execution Plans**: OpenTofu generates an execution plan showing what will change.
- **Resource Graph**: Parallelizes creation of non-dependent resources.
- **Change Automation**: Complex changesets with minimal human interaction.

### Getting help

- [GitHub Discussions](https://github.com/orgs/opentofu/discussions)
- [GitHub Issues](https://github.com/opentofu/opentofu/issues/new/choose)
- [OpenTofu Slack](https://opentofu.org/slack/)

### License

[Mozilla Public License v2.0](https://github.com/opentofu/opentofu/blob/main/LICENSE)

</details>