# Setup Ghoten Action

GitHub Action to install [Ghoten](https://github.com/vmvarela/ghoten) (OpenTofu fork with ORAS backend) in your workflows.

## Usage

### Basic Usage

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Setup Ghoten
    uses: vmvarela/ghoten/action@master

  - name: Run Ghoten
    run: ghoten version
```

### Specific Version

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Setup Ghoten
    uses: vmvarela/ghoten/action@master
    with:
      ghoten_version: '1.12.0'

  - name: Run Ghoten
    run: ghoten init
```

### Version from File

Create a `.ghoten-version` file in your repository:

```
1.12.0
```

Then reference it:

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Setup Ghoten
    uses: vmvarela/ghoten/action@master
    with:
      ghoten_version_file: '.ghoten-version'

  - name: Run Ghoten
    run: ghoten plan
```

### Using .tool-versions (asdf format)

If you use [asdf](https://asdf-vm.com/) or similar tools, you can use `.tool-versions`:

```
ghoten 1.12.0
terraform 1.5.0
```

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Setup Ghoten
    uses: vmvarela/ghoten/action@master
    with:
      ghoten_version_file: '.tool-versions'
```

### Complete Example

```yaml
name: Infrastructure

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  terraform:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Ghoten
        id: setup
        uses: vmvarela/ghoten/action@master
        with:
          ghoten_version: 'latest'

      - name: Print version
        run: echo "Installed Ghoten version ${{ steps.setup.outputs.version }}"

      - name: Tofu Init
        run: ghoten init

      - name: Tofu Plan
        run: ghoten plan

      - name: Tofu Apply
        if: github.ref == 'refs/heads/main'
        run: ghoten apply -auto-approve
```

## Inputs

| Input | Description | Default |
|-------|-------------|---------|
| `ghoten_version` | Version to install (`latest` or specific like `1.12.0`) | `latest` |
| `ghoten_version_file` | Path to file containing version (`.ghoten-version` or `.tool-versions`) | - |
| `github_token` | GitHub token for API requests (avoids rate limiting) | `${{ github.token }}` |

> **Note**: `ghoten_version_file` takes precedence over `ghoten_version` if both are specified.

## Outputs

| Output | Description |
|--------|-------------|
| `version` | The version of Ghoten that was installed (without `v` prefix) |

## Version File Formats

### .ghoten-version

Plain text file with just the version:

```
1.12.0
```

### .tool-versions

[asdf](https://asdf-vm.com/) compatible format:

```
ghoten 1.12.0
nodejs 20.0.0
```

## Supported Platforms

| OS | Architectures |
|----|---------------|
| Linux | x64, x86, arm64, arm |
| macOS | x64, arm64 |
| Windows | x64, x86 |

## Using with ORAS Backend

Ghoten includes the ORAS backend for storing state in OCI registries. Example configuration:

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/myorg/myrepo/terraform-state"
  }
}
```

See the [ORAS backend documentation](https://github.com/vmvarela/ghoten/tree/master/internal/backend/remote-state/oras) for more details.

## License

This project is licensed under the Mozilla Public License 2.0 - see the [LICENSE](../LICENSE) file.
