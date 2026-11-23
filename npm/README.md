# kinerouter

npm delivery shim for [KineRouter](https://github.com/keyzakyy-dev/kinerouter) — a
single-binary AI gateway with an embedded dashboard.

```bash
npm i -g kinerouter
```

npm 11+ blocks postinstall scripts by default. If the binary download is
skipped, allow it once:

```bash
npm i -g --allow-scripts=kinerouter kinerouter
```

The postinstall step downloads the release binary for your platform
(darwin/linux/windows, amd64/arm64) from GitHub Releases, verifies it against
the release's `checksums.txt`, and drops it in this package's `bin/`. The
`kinerouter` command then execs that binary.

## Use

```bash
INITIAL_PASSWORD='choose-something' kinerouter       # serves on :20128
kinerouter connect http://localhost:20128 --tools all   # point local CLIs at it
kinerouter --help
```

Data lives in `~/.kinerouter` (`%APPDATA%\kinerouter` on Windows); set
`DATA_DIR` to put it elsewhere.

## Notes

- No runtime dependencies; the Go binary is static.
- Pin a release: `KINEROUTER_VERSION=0.1.0 npm i -g kinerouter`.
- Offline install: `KINEROUTER_SKIP_DOWNLOAD=1 npm i -g kinerouter`, then set
  `KINEROUTER_BIN=/path/to/kinerouter`.
- Only `linux`, `darwin` and `win32` on `x64`/`arm64` are supported; other
  platforms get a message pointing at the release page.

MIT — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
