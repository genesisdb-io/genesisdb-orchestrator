# GenesisDB orchestrator

A small cross-platform CLI for running isolated GenesisDB Docker containers behind one local HTTPS proxy.

Each instance is available at both:

- `https://<name>.genesisdb.local` on port 443
- `http://<name>.genesisdb.local` on port 80

HTTPS is recommended for browsers and applications that use the operating system trust store. The HTTP endpoint exists for local development tools whose embedded Node.js runtime does not use the operating system trust store.

The GenesisDB container itself listens on its standard port 8080 inside a private Docker network. Instance data is stored in a named Docker volume.

## Requirements

- Docker Desktop or Docker Engine
- Administrator access for the hosts file and local certificate trust store
- On Linux, `update-ca-certificates` or `trust`

## Installation

Install the latest release on macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/genesisdb-io/genesisdb-orchestrator/main/install.sh | bash
```

The installer detects the operating system and architecture, verifies the release checksum, and installs into the first writable directory among `/usr/local/bin`, `~/.local/bin`, and `~/bin`. Pass a release tag and destination to override the defaults:

```sh
curl -fsSL https://raw.githubusercontent.com/genesisdb-io/genesisdb-orchestrator/main/install.sh | bash -s -- v0.0.2 ~/bin
```

Alternatively, download an archive from the [latest GitHub release](https://github.com/genesisdb-io/genesisdb-orchestrator/releases/latest). Release archives are available for macOS, Linux, and Windows on AMD64 and ARM64. Windows archives contain `genesisdb.exe`.

## Project structure

```text
cmd/genesisdb/          Application entry point and build version
internal/cli/           Command parsing, terminal output, and wizard
internal/orchestrator/  Docker lifecycle, API, backups, certificates, and OS integration
internal/ui/            Bubble Tea database dashboard
internal/updater/       Release checks, checksum validation, and self-update
```

The entry point only handles process exit behavior. CLI concerns and orchestration logic remain independently testable in internal packages.

## Build

```sh
make build
```

Build release binaries for macOS, Linux, and Windows locally:

```sh
make release VERSION=1.0.0
```

Local builds default to version `0.0.0`. Published builds receive their version from the Git tag through GoReleaser.

## Dashboard

Run `genesisdb` without arguments in a terminal to open the interactive dashboard (`genesisdb ui` still works as an alias):

```sh
genesisdb
```

The dashboard initializes the local proxy and starts existing databases automatically when it opens. Administrator access is requested when certificate or hosts-file setup requires it. It then lists running and stopped databases and refreshes automatically. Select an instance with the arrow keys or `j` and `k`. Available actions:

- `c`: create a database with name, visible auth token, and optional license fields
- `s`: start or stop the selected database
- `p`: initialize everything or shut down all databases and the proxy
- `e`: export a JSON backup through `GET /api/v1/backup/create`
- `i`: restore a JSON backup through `POST /api/v1/backup/restore`
- `d`: permanently delete the database after confirmation
- `r`: refresh containers and `GET /api/v1/status` data
- `q`: close the dashboard

The details panel displays engine, event store, system, storage, memory, CPU, and license information from `/api/v1/status`. Backup restore only works with an empty target event store, as required by GenesisDB.

The dashboard keeps a fixed terminal-sized canvas and pauses automatic Docker polling while a dialog or lifecycle operation is active. This avoids redraw jitter and prevents background refreshes from replacing action or validation errors.

## Usage

Initialize the proxy, create the wildcard certificate, trust its local CA, add `genesisdb.local` to the hosts file, and start all existing GenesisDB instances:

```sh
genesisdb init                      # macOS/Linux, sudo is requested when needed
# Run an Administrator terminal on Windows, then:
genesisdb init
```

Do not run the whole command with `sudo` on macOS or Linux. The CLI stores state for the current user and invokes `sudo` only for certificate and hosts-file changes.

Create an instance with the interactive wizard. The auth token remains visible while typing. The license key is hidden and optional, so press Enter to use the free license:

```sh
genesisdb create
```

You can also provide values directly, or provide some values and let the wizard prompt for the missing required values:

```sh
genesisdb create <name> --auth-token secret --license-key license
genesisdb create <name> --auth-token secret                         # free license
genesisdb create <name> --auth-token secret --license-key ""        # free license
genesisdb create <name>                                             # interactive secrets
```

Stop or permanently delete one instance:

```sh
genesisdb stop <name>
genesisdb delete <name>
```

Shut down every managed GenesisDB instance and the proxy, then start all of them again:

```sh
genesisdb shutdown
genesisdb init
```

`delete` removes the container, its named data volume, proxy route, and hosts-file entry. `shutdown` preserves containers and data. Creating, stopping, and deleting instances refuse to run while the proxy is not initialized and running.

### VS Code extension

For an orchestrated local instance, configure the GenesisDB VS Code extension with:

```text
http://<name>.genesisdb.local
```

The extension host performs its connection test with Node.js `fetch`. Node.js does not reliably read locally trusted certificate authorities from the macOS Keychain, so the HTTPS URL can fail with `UNABLE_TO_VERIFY_LEAF_SIGNATURE` even though browsers and the orchestrator can connect. Published GenesisDB HTTPS endpoints continue to work because their certificates use a public certificate authority.

## Updates

Check for or install a new CLI release:

```sh
genesisdb update --check
genesisdb update
```

Published builds automatically check GitHub at most once every 12 hours when running lifecycle commands. If a newer release exists, the CLI prints a short notice. Network failures never prevent normal GenesisDB commands from running. Local development builds report version `0.0.0` and cannot self-update.

The updater verifies the selected release archive against `checksums.txt` before replacing the current executable. The executable must be writable by the current user.

## Docker resources

The CLI creates:

- Network: `genesisdb-local`
- Proxy container: `genesisdb-local-proxy` using `caddy:2-alpine`
- Instance containers: `genesisdb-local-<name>`
- Data volumes: `genesisdb-local-<name>-data`

CLI state and generated certificates are kept in the operating system's user configuration directory under `genesisdb`.

## License

MIT
