# gh-cost-center

[![CI](https://github.com/renan-alm/gh-cost-center/actions/workflows/ci.yml/badge.svg)](https://github.com/renan-alm/gh-cost-center/actions/workflows/ci.yml)
[![Release](https://github.com/renan-alm/gh-cost-center/actions/workflows/release.yml/badge.svg)](https://github.com/renan-alm/gh-cost-center/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/renan-alm/gh-cost-center)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/renan-alm/gh-cost-center)](https://github.com/renan-alm/gh-cost-center/releases/latest)

A [GitHub CLI](https://cli.github.com/) extension to automate [GitHub Cost Center](https://docs.github.com/en/billing/concepts/cost-centers) creation and syncing for your enterprise.

Originally based on [GitHub Cost Center Automation](https://github.com/github/cost-center-automation) project.

- **Users (PRU) Mode**: Simple two-tier model (PRU overages allowed / not allowed)
- **Teams Mode**: Automatic assignment based on GitHub team membership
- **Repos Mode**: Assign repositories to cost centers via explicit property→CC mappings (OR logic)
- **Custom-Prop Mode**: Multi-filter cost centers using AND logic across custom properties
- **Budget Creation**: Automatically create Copilot PRU and Actions budgets

## Installation

```bash
gh extension install renan-alm/gh-cost-center
```

Requires a GitHub token with enterprise billing admin access (see [Authentication](#authentication) below).

## Quick Start

```bash
# Copy and edit configuration
cp config/config.example.yaml config/config.yaml

# Preview PRU-based assignments (no changes made)
gh cost-center assign --mode plan

# Apply PRU-based assignments
gh cost-center assign --mode apply --yes
```

## Usage

### Assign Cost Centers

The active mode is set via `cost_center.mode` in your config YAML.

```bash
# Preview assignments (any mode — reads from config)
gh cost-center assign --mode plan

# Apply assignments
gh cost-center assign --mode apply --yes

# Auto-create cost centers and budgets
gh cost-center assign --mode apply --yes --create-cost-centers --create-budgets
```

### Other Commands

```bash
# View resolved configuration
gh cost-center config

# List Copilot licence holders
gh cost-center list-users

# Generate summary report
gh cost-center report

# Cache management
gh cost-center cache --stats
gh cost-center cache --clear
gh cost-center cache --cleanup

# Version
gh cost-center version
```

### Cache

Cost center lookups are cached in `.cache/cost_centers.json` with a 24-hour TTL to reduce API calls on repeated runs.

## Authentication

The CLI resolves a GitHub token using the first available source (in order):

| Priority | Source | Example |
|----------|--------|---------|
| 1 | `--token` flag | `gh cost-center assign --token ghp_xxx ...` |
| 2 | `GITHUB_TOKEN` env var | `export GITHUB_TOKEN=ghp_xxx` |
| 3 | `GH_TOKEN` env var | `export GH_TOKEN=ghp_xxx` |
| 4 | `gh auth token` (shell-out) | Automatic if `gh auth login` was run |

### `.env` file support

A `.env` file in the working directory is loaded automatically. Existing environment variables are **not** overwritten — session values always take precedence.

```bash
# .env
GITHUB_TOKEN=ghp_xxx
GITHUB_ENTERPRISE=your-enterprise
```

## Configuration

Copy the example and edit:

```bash
cp config/config.example.yaml config/config.yaml
```

Run `gh cost-center config` to verify the resolved values.

### Users (PRU) Mode

```yaml
github:
  enterprise: "your-enterprise"

cost_center:
  mode: "users"
  users:
    auto_create: true
    no_prus_cost_center_name: "00 - No PRU overages"
    prus_allowed_cost_center_name: "01 - PRU overages allowed"
    exception_users:
      - "alice"
      - "bob"
```

### Teams Mode

```yaml
github:
  enterprise: "your-enterprise"
  organizations:
    - "your-org"

cost_center:
  mode: "teams"
  teams:
    scope: "organization"   # or "enterprise"
    strategy: "auto"         # one cost center per team
    auto_create: true
    remove_unmatched_users: true
```

Cost center naming:
- Organization scope: `[org team] {org}/{team}`
- Enterprise scope: `[enterprise team] {team}`

When `auto_create: false`, cost center names are **resolved** to UUIDs via the billing API (not created). If any name cannot be found, the sync aborts with an actionable error. This applies to both `auto` and `manual` strategies.

In `manual` strategy, mapping values accept either a **display name** (resolved via the billing API) or a **UUID** (used directly, no lookup).

### Repos Mode

```yaml
github:
  enterprise: "your-enterprise"
  organizations:
    - "your-org"

cost_center:
  mode: "repos"
  repos:
    mappings:
      - cost_center: "Platform Engineering"
        property_name: "team"
        property_values: ["platform", "infrastructure"]
      - cost_center: "Production Services"
        property_name: "environment"
        property_values: ["production"]
```

### Custom-Prop Mode

```yaml
github:
  enterprise: "your-enterprise"
  organizations:
    - "your-org"

cost_center:
  mode: "custom-prop"
  custom_prop:
    cost_centers:
      - name: "Backend Engineering"
        filters:
          - property: "team"
            value: "backend"
          - property: "cost-center-id"
            value: "CC-1234"
```

### Budget Configuration

```yaml
budgets:
  enabled: true
  products:
    copilot:
      amount: 100
      enabled: true
    actions:
      amount: 125
      enabled: true
```

Use `--create-budgets` with any assign command to create budgets automatically.

### GitHub Enterprise Data Resident / GHES

```yaml
github:
  enterprise: "your-enterprise"
  # GHE Data Resident
  api_base_url: "https://api.octocorp.ghe.com"
  # — or GHES —
  # api_base_url: "https://github.company.com/api/v3"
```

## Exit Codes

| Code | Meaning |
|------|---------|
| `0`  | All operations completed successfully |
| `1`  | One or more operations failed (partial assignment failures, budget creation errors, I/O errors, invalid configuration) |

Partial failures (e.g., 2 of 10 users failed to assign) produce exit code `1` with a summary message indicating the count. This ensures CI/CD pipelines detect incomplete runs.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| 401 / 403 errors | Ensure a valid token is available via `--token`, `GITHUB_TOKEN`, `GH_TOKEN`, `.env`, or `gh auth login`. The token must have enterprise billing admin access. |
| No teams found | Verify account has `read:org` access for the target orgs |
| Cost center creation fails | Ensure enterprise billing admin permissions |
| Cost center not found (404) with `auto_create: false` | Cost center names are resolved to UUIDs via the API. If a name can't be found, the sync aborts with an error listing unresolved names. Verify the name matches exactly in **Settings → Billing → Cost Centers**, or enable `auto_create: true`. In `manual` strategy you can also use a UUID directly as the mapping value to bypass name resolution. |
| Special characters in cost center names (ü, ö, ä) | Names with non-ASCII characters work correctly — they are resolved to UUIDs before API calls, so special characters never appear in API URLs. |
| Exit code 1 on partial failures | Expected behavior — some user assignments or budget creations failed. Check the error summary for details. |
| Budget API unavailable (404) | The Budgets API may not be enabled for your enterprise. Budget creation is skipped gracefully with a warning. |

Enable debug logging:

```bash
gh cost-center assign --mode plan --verbose
```

Logs are written to the path configured in `logging.file` (default: `logs/cost_centers.log`).

## Contributing

1. Fork this repository and create a branch (`feat/<name>`)
2. Make focused changes with clear commit messages
3. Ensure `go vet ./...` and `go test -race ./...` pass
4. Submit a PR with a description and link to related issues

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
