# Quick Configuration Guide

This guide walks through the most common `gh-cost-center` configuration scenarios. Every example assumes you have already:

1. Installed the extension: `gh extension install renan-alm/gh-cost-center`
2. Created your config file: `cp config/config.example.yaml config/config.yaml`
3. Set up authentication (see [README → Authentication](README.md#authentication))

All examples use `--mode plan` (dry-run). Replace with `--mode apply --yes` when you are ready to push changes.

---

## Table of Contents

- [1. Users / PRU-Based (Default)](#1-users--pru-based-default)
- [2. Teams — Auto Strategy](#2-teams--auto-strategy)
- [3. Teams — Manual Strategy](#3-teams--manual-strategy)
- [4. Repos (Explicit Mappings)](#4-repos-explicit-mappings)
- [5. Custom-Prop (AND Filters)](#5-custom-prop-and-filters)
- [6. Complex Scenarios](#6-complex-scenarios)
  - [6a. Teams + Budgets + Auto-Create](#6a-teams--budgets--auto-create)
  - [6b. Multi-Org Manual Teams Mapping](#6b-multi-org-manual-teams-mapping)
  - [6c. Repos Mode with Multiple Properties](#6c-repos-mode-with-multiple-properties)
  - [6d. Custom-Prop with AND Filters + Budgets](#6d-custom-prop-with-and-filters--budgets)
  - [6e. GHE Data Resident / GHES with Teams](#6e-ghe-data-resident--ghes-with-teams)

---

## 1. Users / PRU-Based (Default)

The simplest mode. All Copilot users go to a "no PRU overages" cost center, except for a list of exception users who go to a "PRU overages allowed" cost center.

### Config

```yaml
github:
  enterprise: "my-enterprise"

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

### Run

```bash
gh cost-center assign --mode plan
gh cost-center assign --mode apply --yes
```

### What happens

| User | Assigned to |
|------|-------------|
| alice | 01 - PRU overages allowed |
| bob | 01 - PRU overages allowed |
| everyone else | 00 - No PRU overages |

---

## 2. Teams — Auto Strategy

One cost center is created automatically for each GitHub team. Users are assigned to the cost center that matches their team membership.

### Config

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "teams"
  teams:
    scope: "organization"      # query teams from organizations
    strategy: "auto"           # one cost center per team (auto-named)
    auto_create: true
    remove_unmatched_users: true
```

### Run

```bash
gh cost-center assign --mode plan
```

### Cost center naming

| Scope | Naming pattern | Example |
|-------|---------------|---------|
| `organization` | `[org team] {org}/{team}` | `[org team] my-org/frontend` |
| `enterprise` | `[enterprise team] {team}` | `[enterprise team] platform` |

> **Tip:** When `auto_create: false`, cost centers are resolved by name instead of created. The sync aborts if any auto-generated name doesn't match an existing cost center.

---

## 3. Teams — Manual Strategy

You control exactly which team maps to which cost center name.

### Config

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "teams"
  teams:
    scope: "organization"
    strategy: "manual"
    auto_create: true
    remove_unmatched_users: true
    mappings:
      "my-org/frontend": "CC-Frontend"
      "my-org/backend": "CC-Backend"
      "my-org/infra": "CC-Platform"
```

### Run

```bash
gh cost-center assign --mode plan
```

### What happens

| Team | Cost center |
|------|-------------|
| my-org/frontend | CC-Frontend |
| my-org/backend | CC-Backend |
| my-org/infra | CC-Platform |
| Teams not listed | Skipped |

### Using `auto_create: false`

When you set `auto_create: false`, cost centers are **not** created — names are resolved to UUIDs via the billing API. If any name doesn't match an existing cost center, the sync aborts with an actionable error.

This is useful when cost centers are pre-created by a billing admin and the tool should only assign users, never create new cost centers.

```yaml
cost_center:
  mode: "teams"
  teams:
    strategy: "manual"
    auto_create: false          # resolve names → UUIDs, never create
    mappings:
      "my-org/frontend": "CC-Frontend"
      "my-org/backend": "CC-Backend"
```

> **Mapping values** accept either a **display name** or a **UUID**:
> - **Name** (recommended): the tool looks up the name in the billing API and resolves it to a UUID automatically. Supports `auto_create: true`. Works with special characters (ü, ö, ä, etc.).
> - **UUID**: used directly as the cost center ID — no API lookup performed. Useful when you already have the UUID from the GitHub billing settings and want to bypass name resolution.

---

## 4. Repos (Explicit Mappings)

Assign **repositories** (not users) to cost centers based on organization custom property values. Each mapping uses **OR logic** — a repository matches if its property value is any of the listed values.

> **Prerequisite:** Configure [custom properties](https://docs.github.com/en/organizations/managing-organization-settings/managing-custom-properties-for-repositories-in-your-organization) on your GitHub organization repositories first.

### Config

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "repos"
  repos:
    mappings:
      - cost_center: "Platform Engineering"
        property_name: "team"
        property_values:
          - "platform"
          - "infrastructure"

      - cost_center: "Product"
        property_name: "team"
        property_values:
          - "product"
```

### Run

```bash
gh cost-center assign --mode plan
```

### Notes

- Property names and values are **case-sensitive**.
- Repositories without the specified property are skipped.
- A repository can match multiple mappings.

---

## 5. Custom-Prop (AND Filters)

Assign **repositories** to cost centers using fine-grained custom-property filters with **AND logic**. Unlike Repos mode (section 4), which matches any value from a list (OR logic), custom-prop cost centers require a repository to satisfy **every** filter simultaneously.

> **Prerequisite:** Configure [custom properties](https://docs.github.com/en/organizations/managing-organization-settings/managing-custom-properties-for-repositories-in-your-organization) on your GitHub organization repositories first.

### Config — Single Filter

The simplest case: match repositories on a single custom property.

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "custom-prop"
  custom_prop:
    cost_centers:
      - name: "Backend Engineering"
        filters:
          - property: "team"
            value: "backend"

      - name: "Frontend Engineering"
        filters:
          - property: "team"
            value: "frontend"

      - name: "Data & ML"
        filters:
          - property: "team"
            value: "data"
```

### Config — Multiple Filters (AND Logic)

Combine multiple filters in a single cost center entry. A repository must match **all** filters to be included:

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "custom-prop"
  custom_prop:
    cost_centers:
      - name: "Production Backend"
        filters:
          - property: "team"
            value: "backend"
          - property: "environment"
            value: "production"

      - name: "Staging Backend"
        filters:
          - property: "team"
            value: "backend"
          - property: "environment"
            value: "staging"

      - name: "Production Frontend"
        filters:
          - property: "team"
            value: "frontend"
          - property: "environment"
            value: "production"
```

### Run

```bash
gh cost-center assign --mode plan
gh cost-center assign --mode apply --yes
```

### What happens

Given the multiple-filter config above:

| Repository properties | Assigned to |
|---|---|
| `team=backend`, `environment=production` | Production Backend |
| `team=backend`, `environment=staging` | Staging Backend |
| `team=frontend`, `environment=production` | Production Frontend |
| `team=frontend`, `environment=staging` | Skipped (no matching cost center) |
| `team=backend` (no `environment`) | Skipped (missing required filter) |

### Notes

- All filters in a single cost center use **AND** logic — every filter must match.
- For **OR** logic across different property combinations, add separate cost center entries.
- Property names and values are **case-sensitive**.
- Cost centers are auto-created if they don't exist.

---

## 6. Complex Scenarios

### 6a. Teams + Budgets + Auto-Create

Automatically create one cost center per team **and** provision Copilot and Actions budgets for each new cost center.

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "teams"
  teams:
    scope: "organization"
    strategy: "auto"
    auto_create: true
    remove_unmatched_users: true

budgets:
  enabled: true
  products:
    copilot:
      amount: 200       # USD per cost center
      enabled: true
    actions:
      amount: 150
      enabled: true
```

```bash
gh cost-center assign --mode plan --create-cost-centers --create-budgets
```

---

### 6b. Multi-Org Manual Teams Mapping

Your enterprise has multiple organizations and you want to map teams from each org to shared cost centers.

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "org-alpha"
    - "org-beta"

cost_center:
  mode: "teams"
  teams:
    scope: "organization"
    strategy: "manual"
    auto_create: true
    remove_unmatched_users: true
    mappings:
      # Alpha org
      "org-alpha/backend": "CC-Engineering"
      "org-alpha/frontend": "CC-Engineering"
      "org-alpha/data": "CC-Data"
      # Beta org — maps into the same cost centers
      "org-beta/api-team": "CC-Engineering"
      "org-beta/analytics": "CC-Data"
```

```bash
gh cost-center assign --mode plan --create-cost-centers
```

Users from both organizations are consolidated into shared cost centers (`CC-Engineering`, `CC-Data`).

---

### 6c. Repos Mode with Multiple Properties

Map repositories using different custom properties to different cost centers. For example, separate by both `team` and `environment`.

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "repos"
  repos:
    mappings:
      # By team
      - cost_center: "Platform Engineering"
        property_name: "team"
        property_values: ["platform", "infrastructure", "devops"]

      - cost_center: "Frontend Applications"
        property_name: "team"
        property_values: ["web", "mobile", "ui"]

      # By environment
      - cost_center: "Production Services"
        property_name: "environment"
        property_values: ["production"]

      - cost_center: "Staging"
        property_name: "environment"
        property_values: ["staging", "qa"]
```

```bash
gh cost-center assign --mode plan --create-cost-centers
```

> A repository with `team=platform` **and** `environment=production` will match **both** "Platform Engineering" and "Production Services".

---

### 6d. Custom-Prop with AND Filters + Budgets

Use custom-prop cost centers with AND filters and automatic budget creation. Ideal for enterprises that track both team ownership and a billing code on each repository.

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

cost_center:
  mode: "custom-prop"
  custom_prop:
    cost_centers:
      - name: "CC-1234 — Backend"
        filters:
          - property: "team"
            value: "backend"
          - property: "cost-center-id"
            value: "CC-1234"

      - name: "CC-5678 — Frontend"
        filters:
          - property: "team"
            value: "frontend"
          - property: "cost-center-id"
            value: "CC-5678"

      - name: "CC-9999 — Data Platform"
        filters:
          - property: "team"
            value: "data"
          - property: "cost-center-id"
            value: "CC-9999"

budgets:
  enabled: true
  products:
    copilot:
      amount: 300
      enabled: true
    actions:
      amount: 200
      enabled: true
```

```bash
gh cost-center assign --mode plan --create-cost-centers --create-budgets
```

Each cost center only includes repositories where **both** the `team` and `cost-center-id` properties match. Budgets are created for each new cost center.

---

### 6e. GHE Data Resident / GHES with Teams

If your enterprise runs on GitHub Enterprise Data Resident or a self-hosted GitHub Enterprise Server, set `api_base_url` and use any mode as normal.

```yaml
github:
  enterprise: "my-enterprise"
  organizations:
    - "my-org"

  # GHE Data Resident
  api_base_url: "https://api.octocorp.ghe.com"

  # — or GHES (self-hosted) —
  # api_base_url: "https://github.company.com/api/v3"

cost_center:
  mode: "teams"
  teams:
    scope: "organization"
    strategy: "auto"
    auto_create: true
```

```bash
gh cost-center assign --mode plan
```

---

## Quick Reference: Key Flags

| Flag | Purpose |
|------|---------|
| `--mode plan` | Dry-run — preview changes without applying |
| `--mode apply` | Push changes to GitHub |
| `--yes` / `-y` | Skip confirmation prompt in apply mode |
| `--create-cost-centers` | Create cost centers that don't exist yet |
| `--create-budgets` | Create budgets for new cost centers |
| `--incremental` | Only process users added since last run (users mode) |
| `--check-current` | Check current cost center membership before assigning |
| `--token <PAT>` | Pass a GitHub token directly |
| `--config <path>` | Use a custom config file path |
| `--verbose` / `-v` | Enable debug logging |

> **Note:** The active mode (users, teams, repos, custom-prop) is determined by `cost_center.mode` in your config file, not by CLI flags.

---

## Verifying Your Config

Before running an assignment, check the resolved configuration:

```bash
gh cost-center config
```

This shows the final merged values after applying environment variables, `.env` files, and YAML defaults.
