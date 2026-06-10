# UnitSense Agent

A lightweight, open-source agent that reads session files produced by AI coding tools (Claude Code, Codex CLI) on developer machines, computes aggregate metrics, and posts them to UnitSense.

**Status:** v0.1.0 — Claude Code + Codex CLI on macOS, Linux, and Windows.

## Quick start

### macOS / Linux

```bash
# Install (verifies cosign signature against pinned identity)
curl -fsSL https://app.unitsense.ai/install-agent.sh | bash

# Configure (prompts for tenant slug, email, registration token)
unitsense-agent setup

# Register a cron / scheduled task to run every 10 minutes
unitsense-agent install --schedule=10m
```

### Windows (PowerShell)

```powershell
# Install
irm https://app.unitsense.ai/install-agent.ps1 | iex

# Configure
unitsense-agent.exe setup

# Schedule to run every 10 minutes via Task Scheduler
unitsense-agent.exe install --schedule=10m
```

## What gets sent

The agent ships **aggregates only**, never raw content. See the design doc for the exact contract.

## License

MIT
