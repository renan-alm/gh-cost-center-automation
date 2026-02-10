# Copilot Instructions for Cost Center Automation

## Project Overview

This is a Python CLI tool that automates GitHub Copilot cost center assignments for GitHub Enterprise. It operates in three modes:
- **PRU-based**: Two-tier model (PRU overages allowed/not allowed) via exception lists
- **Teams-based**: Assigns users based on GitHub team membership (org or enterprise scope)
- **Repository-based**: Assigns repos to cost centers based on custom properties

## Architecture

```
main.py                          # CLI entry point, argument parsing, mode routing
src/
├── config_manager.py            # Loads YAML config + env vars, validates settings
├── config_models.py             # Data classes for config sections (e.g., RepositoryConfig)
├── github_api.py                # GitHub API client (GitHubCopilotManager) - all API calls
├── cost_center_manager.py       # PRU-based assignment logic
├── teams_cost_center_manager.py # Teams-based assignment logic
├── repository_cost_center_manager.py # Repository-based assignment logic
└── logger_setup.py              # Logging configuration
```

### Key Data Flow
1. `main.py` parses args → creates `ConfigManager` → creates `GitHubCopilotManager`
2. Mode-specific manager (`CostCenterManager`, `TeamsCostCenterManager`, or `RepositoryCostCenterManager`) is instantiated
3. Manager fetches data via `GitHubCopilotManager`, computes assignments, optionally applies via API

## Developer Workflow

```bash
# Setup
pip install -r requirements.txt
cp config/config.example.yaml config/config.yaml

# Required environment variables
export GITHUB_TOKEN="your_token"
export GITHUB_ENTERPRISE="your-enterprise-slug"

# Run commands (always preview with plan first)
python main.py --assign-cost-centers --mode plan           # PRU mode preview
python main.py --assign-cost-centers --mode apply --yes    # PRU mode apply
python main.py --teams-mode --assign-cost-centers --mode plan  # Teams mode
python main.py --show-config                               # Verify config
```

## Code Conventions

### Configuration Pattern
- Config sources: `config/config.yaml` → environment variables (env takes precedence)
- Access via `ConfigManager` properties (e.g., `config.github_enterprise`, `config.teams_scope`)
- Backward compatibility: support old config keys alongside new ones (see `config_manager.py` L90-110)

### API Calls
All GitHub API interactions go through `GitHubCopilotManager` in [src/github_api.py](src/github_api.py):
- Uses `requests.Session` with retry logic (429, 5xx handling)
- Pagination handled internally (see `get_copilot_users()`)
- GHE Data Resident support via `api_base_url` config

### Manager Pattern
Each mode has a dedicated manager class that:
- Takes `config` and `github_manager` in constructor
- Has a main entry method (`sync_team_assignments()`, `run()`, `bulk_assign_cost_centers()`)
- Implements `plan` vs `apply` modes internally
- Uses caching for API-fetched data (e.g., `self.teams_cache`, `self.members_cache`)

### Logging
- Use `logging.getLogger(__name__)` in each module
- Levels: `INFO` for progress, `DEBUG` for detailed operations, `WARNING` for skipped items
- User-facing output uses `print()` with emoji indicators (✅, ❌, 📊)

## Adding a New Mode

1. Create `src/new_mode_manager.py` following pattern of `teams_cost_center_manager.py`
2. Add config section to `ConfigManager` with validation
3. Add CLI flag in `main.py` `parse_arguments()`
4. Add handler function `_handle_new_mode()` in `main.py`
5. Wire up in `main()` conditional flow

## Common Pitfalls

- **Cost center IDs vs names**: Auto-created cost centers use names; existing ones need UUIDs
- **Single assignment**: Users can only be in ONE cost center - assignments are mutually exclusive
- **Rate limiting**: Built-in retry handles 429s, but watch for long-running operations
- **Plan mode safety**: Always default to `plan` mode; require explicit `--yes` for `apply`

## Testing

No test framework is configured. Run manual validation:
```bash
python main.py --show-config  # Verify configuration
python main.py --list-users   # Check API connectivity
python main.py --assign-cost-centers --mode plan  # Dry-run assignments
```
