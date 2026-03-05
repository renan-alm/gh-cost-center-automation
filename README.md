# gh-cost-center

[![CI](https://github.com/renan-alm/gh-cost-center/actions/workflows/ci.yml/badge.svg)](https://github.com/renan-alm/gh-cost-center/actions/workflows/ci.yml)
[![Release](https://github.com/renan-alm/gh-cost-center/actions/workflows/release.yml/badge.svg)](https://github.com/renan-alm/gh-cost-center/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/renan-alm/gh-cost-center)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/renan-alm/gh-cost-center)](https://github.com/renan-alm/gh-cost-center/releases/latest)

A [GitHub CLI](https://cli.github.com/) extension to automate [GitHub Cost Center](https://docs.github.com/en/billing/concepts/cost-centers) creation and syncing for your enterprise.

Originally based on [GitHub Cost Center Automation](https://github.com/github/cost-center-automation) project.

- **PRU-Based Mode**: Simple two-tier model (PRU overages allowed / not allowed)
- **Teams-Based Mode**: Automatic assignment based on GitHub team membership
- **Repository-Based Mode**: Assign repositories to cost centers via custom properties
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

```bash
# PRU-based (default)
gh cost-center assign --mode plan
gh cost-center assign --mode apply --yes

# Teams-based
gh cost-center assign --teams --mode plan
gh cost-center assign --teams --mode apply --yes

# Repository-based
gh cost-center assign --repo --mode plan
gh cost-center assign --repo --mode apply --yes

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
gh cost-center report --teams

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

### PRU-Based Mode

```yaml
github:
  enterprise: "your-enterprise"

cost_centers:
  auto_create: true
  no_prus_cost_center_name: "00 - No PRU overages"
  prus_allowed_cost_center_name: "01 - PRU overages allowed"
  prus_exception_users:
    - "alice"
    - "bob"
```

### Teams-Based Mode

```yaml
teams:
  enabled: true
  scope: "organization"   # or "enterprise"
  mode: "auto"             # one cost center per team
  organizations:
    - "your-org"
  auto_create_cost_centers: true
  remove_users_no_longer_in_teams: true
```

Cost center naming:
- Organization scope: `[org team] {org}/{team}`
- Enterprise scope: `[enterprise team] {team}`

### Repository-Based Mode

```yaml
github:
  cost_centers:
    mode: "repository"
    repository_config:
      explicit_mappings:
        - cost_center: "Platform Engineering"
          property_name: "team"
          property_values: ["platform", "infrastructure"]
        - cost_center: "Production Services"
          property_name: "environment"
          property_values: ["production"]

teams:
  organizations:
    - "your-org"   # required for repository mode
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

## Troubleshooting

| Issue | Solution |
|-------|----------|
| 401 / 403 errors | Ensure a valid token is available via `--token`, `GITHUB_TOKEN`, `GH_TOKEN`, `.env`, or `gh auth login`. The token must have enterprise billing admin access. |
| No teams found | Verify account has `read:org` access for the target orgs |
| Cost center creation fails | Ensure enterprise billing admin permissions |

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
