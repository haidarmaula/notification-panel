# New machine setup

This guide takes a new checkout to the complete local application: API, dashboard,
PostgreSQL, Kafka, workers, scheduler, and an isolated provider simulator. The
runner creates local-only credentials and never reads the repository `.env` or
staging credentials. You do not need a host PostgreSQL or Kafka installation,
`migrate`, `sqlc`, Postman, or provider credentials for this flow.
The Python runner uses the standard library, so no Python package installation
or virtual environment is required for `./dev`.

## Platform and tools

The runner is a Bash script that invokes Python and uses Unix `fcntl` locks. It
requires a local Docker Unix socket (`unix://`) and supports `amd64` and `arm64`
Docker architectures. The existing workflow has been checked on macOS arm64;
fresh-machine provisioning was not performed as part of that check. Native Linux
is expected to work but has not been validated here. WSL2 is unverified and may
need Docker Desktop integration configured to expose a local Unix socket. Native
Windows is unsupported.

Install these tools using their official instructions:

| Tool | Requirement | Official installation |
| --- | --- | --- |
| Git | Needed to clone the repository. | [Git downloads](https://git-scm.com/downloads) |
| Bash | Used by `./dev`; available by default on macOS and most Linux distributions. | [GNU Bash](https://www.gnu.org/software/bash/) |
| Docker Engine or Docker Desktop | Must be running locally. On Linux, install Docker Engine and the Compose plugin; on macOS, install Docker Desktop. | [Docker Desktop](https://www.docker.com/products/docker-desktop/) · [Docker Engine](https://docs.docker.com/engine/install/) · [Compose plugin](https://docs.docker.com/compose/install/linux/) |
| Docker Compose | Version 2.24.4 or later. | [Compose installation](https://docs.docker.com/compose/install/) |
| Go | Exactly the version declared in [`go.mod`](../go.mod): Go `1.26.11`. | [Go 1.26.11 downloads](https://go.dev/dl/#go1.26.11) · [Install Go instructions](https://go.dev/doc/install) |
| Python | The runner calls `python3`. The project does not pin its minimum Python release; Python 3.11+ is recommended. The runner was checked with Python 3.14.7. | [Python downloads](https://www.python.org/downloads/) |
| Node.js | The dashboard declares `>=22.12.0`; the full local workflow was checked with 22.23.4. Use Node 22.23.4 for the verified setup; other Node major versions have not been verified here. | [Node.js downloads](https://nodejs.org/en/download) · [Node 22.23.4 archive](https://nodejs.org/dist/v22.23.4/) |
| pnpm | Exactly `11.25.0`, matching the dashboard's `packageManager` pin. | [pnpm installation](https://pnpm.io/installation) |

### Install Node and pnpm

Install Node 22.23.4 from the [official Node archive](https://nodejs.org/dist/v22.23.4/),
or use the optional nvm version manager by following its
[official installation and update instructions](https://github.com/nvm-sh/nvm#installing-and-updating).
nvm is optional; do not assume it is installed.

On a new Mac, Git or the nvm installer may first require Apple's Command Line
Tools. Complete that installation if prompted; see nvm's
[macOS installation notes](https://github.com/nvm-sh/nvm#troubleshooting-on-macos).

After installing nvm, select the tested version before using the repository:

```bash
nvm install 22.23.4
nvm use 22.23.4
```

Install pnpm in the same user environment as Node. With a Node version manager,
do not use `sudo`:

```bash
npm install --global pnpm@11.25.0
```

If `npm` is missing, install or select Node first. For a direct Node installation
whose global directory is not writable, install pnpm into a user-owned prefix:

```bash
npm install --global --prefix "$HOME/.local" pnpm@11.25.0
export PATH="$HOME/.local/bin:$PATH"
pnpm --version
```

Keep that PATH entry in your shell startup file for later terminals. With nvm,
use its selected Node environment instead. See npm's
[permissions guidance](https://docs.npmjs.com/resolving-eacces-permissions-errors-when-installing-packages-globally/)
for user-level installation options; `sudo npm` is not needed.

## Clone and check the tools

Use the repository's actual remote. It is private, so your GitHub account must
have access. Choose SSH or HTTPS:

```bash
git clone git@github.com:haidarmaula/notification-panel.git
# Or: git clone https://github.com/haidarmaula/notification-panel.git
cd notification-panel
```

If you cloned into a different directory name, `cd` into that checkout instead.
In the same terminal that you will use for startup, check that each required
command resolves from that shell's `PATH`, and that Docker can reach its local
daemon:

```bash
bash --version
command -v bash git go python3 node npm pnpm docker
git --version
go version
python3 --version
node --version
npm --version
pnpm --version
docker --version
docker compose version
docker context inspect --format '{{.Endpoints.docker.Host}}'
docker info
```

Confirm Go reports `go1.26.11`, pnpm reports `11.25.0`, Node reports `v22.23.4`
for the verified setup, and Compose is at least `v2.24.4`. The dashboard declares
Node `>=22.12.0`, but other major versions have not been verified here. The
Docker context host must begin with `unix://`; `docker info` must succeed. If
using nvm, run `nvm use 22.23.4` before these checks and keep that terminal active
so its PATH is used by `./dev`. The runner inherits PATH from the shell where it
runs; fix that shell's installation or version-manager setup if `command -v`
cannot find a tool.

## Start the local application

From the repository root, run:

```bash
./dev up
./dev credentials
```

Open <http://127.0.0.1:5173/login> and sign in using the local email and password
printed by `./dev credentials`. During `./dev up`, the runner creates synthetic
credentials and sample data under the ignored `tmp/local-dev/` directory. Keep
that private directory with the Docker volumes; do not commit or share it. No `.env` file or
external OAuth, Firebase, or FCM credentials are needed.

The runner creates the local account settings during `up`; `./dev credentials`
only displays the saved email and password. It does not create or reset them.
`./dev up` also runs a login, application grant, audience, and process check. Run
the read-only check yourself any time with:

```bash
./dev check
```

A successful check prints `Local login, application grant, audience and process
checks passed.`

Browse the local OpenAPI reference at <http://127.0.0.1:5173/docs/>. Reading the
three API guides requires no credentials. For local authentication and **Try it
out**, follow [the API reference instructions](local-development.md#local-api-reference).

The first `up` builds the local binaries, downloads dashboard dependencies and
container images, creates local TLS material, starts the services, applies
migrations, seeds the sample workspace, and verifies login and audience access.
It can take several minutes and requires network access for uncached downloads.
Later starts reuse local data and apply only new migrations.

To inspect or stop the stack:

```bash
./dev status
./dev logs
./dev down
```

`down` stops the local services and host processes but retains the PostgreSQL and
Kafka volumes, generated settings, and credentials. Do not delete those files as
a way to stop the application. See [full local development](local-development.md)
for watcher, smoke-check, and data-lifecycle details.

## Troubleshooting

- **`./dev` cannot find Bash or Python:** if Bash is missing, install it with
  your operating system's package manager before invoking `./dev`; Python 3 must
  also be installed. Then check `bash --version` and `python3 --version` in the
  same terminal. Python 3.11+ is recommended.
- **`nvm: command not found`:** nvm is optional. Install Node 22.23.4 directly
  from the official archive above, or follow the official nvm installation
  instructions and open a new terminal. Then select Node with `nvm use 22.23.4`.
- **`node`, `npm`, or `pnpm` is missing or the version is wrong:** install/select
  Node 22.23.4 and run `command -v node npm pnpm` followed by `node --version`,
  `npm --version`, and `pnpm --version` again in the startup terminal. Install the
  pinned pnpm version with `npm install --global pnpm@11.25.0` if needed. If pnpm
  was just installed, reopen the terminal if its PATH has not refreshed.
- **Docker is not reachable or the context is not local:** start Docker Desktop or
  the Docker Engine service. Select a local context whose endpoint starts with
  `unix://`; clear any remote `DOCKER_HOST` override in that terminal. Confirm
  `docker info` succeeds before retrying `./dev up`.
- **Linux Docker permission denied:** follow Docker's official
  [Linux post-install steps](https://docs.docker.com/engine/install/linux-postinstall/)
  to configure non-root Docker access for your user, then sign out and back in if
  required and confirm `docker info` succeeds. Do not run `./dev` with `sudo`.
- **A port is occupied:** the runner uses loopback ports 5173 and 18090 for the
  dashboard and BFF, plus 18082 for the local TLS gateway. Stop the process that
  owns a conflicting port yourself, then retry `./dev up`; the runner does not
  kill unrelated processes.
- **Startup fails or only some services start:** inspect `./dev status` and
  `./dev logs`, correct the reported tool, port, or download issue, then run
  `./dev down` and retry `./dev up`. If the runner reports that checkout ownership
  has not been initialized, retry `./dev up` first. Stopping preserves local
  settings and volumes.

For optional development tools and workflows, see
[development tools](tooling/development-tools.md).
