
# Development Scripts Documentation

**Version:** 1.0
**Location:** `/scripts`
**Target Environment:** Local Development

---

## Overview

This directory contains utility scripts used to bootstrap and manage the local development environment.

---

## Prerequisites

The following tools must be installed before running any script:

| Tool             | Required | Description                                                 |
| ---------------- | -------- | ----------------------------------------------------------- |
| `tmux`           | ✅        | Terminal multiplexer used to manage the development session |
| `docker`         | ✅        | Container runtime                                           |
| `docker compose` | ✅        | Starts and manages application services                     |
| `make`           | ✅        | Executes project automation commands                        |
| `nvim`           | ✅        | Default editor opened by the development session            |

---

## `dev.sh`

Bootstraps a tmux development session for the project.

### Usage

Start the application services:

```bash
make up
```

Bootstrap the tmux session:

```bash
./scripts/dev.sh
```

---

## Session Layout

When the session is created for the first time, the following windows are initialized.

| Order | Window           | Startup Command       |
| ----: | ---------------- | --------------------- |
|     1 | `nvim`           | `nvim`                |
|     2 | `logs-server`    | `make logs-server`    |
|     3 | `logs-worker`    | `make logs-worker`    |
|     4 | `logs-scheduler` | `make logs-scheduler` |
|     5 | `shell`          | None                  |

---

## Behavior

The script is designed to be idempotent.

On every execution it will:

* Create the tmux session if it does not already exist.
* Create any missing windows.
* Preserve existing windows.
* Restart startup commands only when their panes are idle.
* Start Docker-dependent commands only after Docker Compose services are running.
* Focus the `nvim` window before attaching to the session.
* Attach to the session when executed outside tmux.
* Switch to the existing session when executed from within tmux.

---

## Typical Workflow

Start the application services.

```bash
make up
```

Bootstrap the development session.

```bash
./scripts/dev.sh
```

Stop the application services.

```bash
make down
```

Restart the services.

```bash
make up
```

Run the script again.

```bash
./scripts/dev.sh
```

The existing tmux session is reused, and any idle log windows are restarted automatically.

---

## Changelog

| Version | Date       | Description                                   |
| ------- | ---------- | --------------------------------------------- |
| 1.0     | 2026-07-23 | Initial documentation for development scripts |
