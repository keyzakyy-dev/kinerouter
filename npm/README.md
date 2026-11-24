# kinerouter

npm delivery shim for [KineRouter](https://github.com/keyzakyy-dev/kinerouter) — a
single-binary AI gateway with an embedded dashboard.

```bash
npm i -g kinerouter
```

npm 11+ blocks postinstall scripts by default. If the download was skipped, the
first `kinerouter` run fetches the binary itself; to allow the script up front:

```bash
npm i -g --allow-scripts=kinerouter kinerouter
```

## Where it installs

The gateway and its tray companion land in the KineRouter data dir, not in this
package:

- Linux/macOS: `~/.kinerouter/runtime`
- Windows: `%LOCALAPPDATA%\kinerouter\runtime`

Both files are verified against the release's `checksums.txt`. Keeping them out
of the package directory means `npm i -g kinerouter@latest` can replace the shim
while the gateway is running, and no second PATH entry can shadow the binary.

Set `DATA_DIR` to move that directory, exactly like the gateway itself.

## Use

```bash
INITIAL_PASSWORD='choose-something' kinerouter       # serves on :20128
kinerouter tray                                      # tray icon only
kinerouter connect http://localhost:20128 --tools all   # point local CLIs at it
kinerouter --help
```

With no arguments in a terminal, `kinerouter` serves, then shows the same
arrow-key menu as 9Router (↑/↓ move, Enter confirm, Esc exit): an update line
when a newer release exists (prints `npm i -g kinerouter@latest --prefer-online`
and stops the server first), Web UI, Terminal UI, Hide to Tray and Exit. Hide to Tray also writes the OS login
entry and detaches the gateway, so closing the terminal leaves it running; the
tray's Quit stops it.

## Uninstall

```bash
npm uninstall -g kinerouter
```

The login entry installed by Hide to Tray runs this shim (`node .../cli.js
--tray`), not the downloaded binary, so removing the package disables the entry
too. npm v7+ runs no uninstall scripts, which is why the entry is written this
way. Leftovers, if you also want them gone:

```bash
rm -f  ~/.config/autostart/kinerouter.desktop   # older builds pointed this at ~/.kinerouter/runtime instead
rm -rf ~/.kinerouter                            # runtime binaries, SQLite DB, API keys
```

## Notes

- No runtime dependencies; the Go binary is static.
- Pin a release: `KINEROUTER_VERSION=0.1.4 npm i -g kinerouter`.
- Offline install: `KINEROUTER_SKIP_DOWNLOAD=1 npm i -g kinerouter`, then set
  `KINEROUTER_BIN=/path/to/kinerouter`.
- Only `linux`, `darwin` and `win32` on `x64`/`arm64` are supported.
- Do not also install a release binary next to it. One install path per machine,
  otherwise the first on `PATH` wins and the other updates on its own schedule.

MIT — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
