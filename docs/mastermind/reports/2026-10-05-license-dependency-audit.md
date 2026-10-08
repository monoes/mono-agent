# Dependency license audit

Commit 2e76ad6e; `python3 scripts/licenseaudit/audit.py deps`. Not legal advice.

- Go modules linked into the CLI or the app: 74 (licenses: Apache-2.0, BSD-2-Clause, BSD-3-Clause, BSD-3-Clause+MIT+Public-Domain, ISC, MIT, MPL-2.0)
- npm packages that ship in the app: 111 (licenses: ISC, MIT)
- npm packages that are build tooling only, not distributed: 117 (licenses: Apache-2.0, BSD-2-Clause, BSD-3-Clause, BlueOak-1.0.0, CC0-1.0, ISC, MIT, MIT-0, MPL-2.0)

## Flagged: copyleft or unclassified, in what ships

- `github.com/go-sql-driver/mysql` v1.10.1: MPL-2.0 (cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64)

## Go modules

| Module | Version | License | Built into |
|---|---|---|---|
| filippo.io/edwards25519 | v1.2.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/ProtonMail/go-crypto | v1.5.2 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/PuerkitoBio/goquery | v1.13.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/andybalholm/cascadia | v1.3.4 | BSD-2-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/bwmarrin/discordgo | v0.29.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/clipperhouse/displaywidth | v0.10.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/clipperhouse/uax29/v2 | v2.6.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/cloudflare/circl | v1.6.3 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/danieljoos/wincred | v1.2.3 | MIT | cli windows/amd64, app windows/amd64 |
| github.com/disintegration/imaging | v1.6.2 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/dlclark/regexp2 | v1.11.4 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/dop251/goja | v0.0.0-20260305124333-6a7976c22267 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/dustin/go-humanize | v1.0.1 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/fatih/color | v1.18.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/go-rod/rod | v0.116.2 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/go-sourcemap/sourcemap | v2.1.3+incompatible | BSD-2-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/go-sql-driver/mysql | v1.10.1 | MPL-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/go-telegram-bot-api/telegram-bot-api/v5 | v5.5.1 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/godbus/dbus/v5 | v5.2.2 | BSD-2-Clause | cli linux/amd64, cli linux/arm64, app linux/amd64 |
| github.com/google/pprof | v0.0.0-20260802141513-ef3492d7dac3 | Apache-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/gorilla/websocket | v1.5.3 | BSD-2-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0 | cli windows/amd64 |
| github.com/jlaffaye/ftp | v0.2.4 | ISC | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/klauspost/compress | v1.19.2 | Apache-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/leaanthony/go-ansi-parser | v1.6.1 | MIT | app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/leaanthony/slicer | v1.6.0 | MIT | app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/leaanthony/u | v1.1.1 | MIT | app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/lib/pq | v1.12.3 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/mattn/go-colorable | v0.1.14 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/mattn/go-isatty | v0.0.24 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/mattn/go-runewidth | v0.0.19 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/mmcdole/gofeed | v1.5.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/mmcdole/goxpp/v2 | v2.0.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/ncruces/go-strftime | v1.0.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli windows/amd64, app darwin/arm64, app windows/amd64 |
| github.com/olekukonko/cat | v0.0.0-20250911104152-50322a0618f6 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/olekukonko/errors | v1.2.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/olekukonko/ll | v0.1.6 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/olekukonko/tablewriter | v1.1.5 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/pkg/errors | v0.9.1 | BSD-2-Clause | app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/redis/go-redis/v9 | v9.22.0 | BSD-2-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/rivo/uniseg | v0.4.7 | MIT | app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/robfig/cron/v3 | v3.0.1 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/rs/zerolog | v1.35.1 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/slack-go/slack | v0.30.1 | BSD-2-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/spf13/cobra | v1.10.2 | Apache-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/spf13/pflag | v1.0.9 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/wailsapp/go-webview2 | v1.0.22 | MIT | app windows/amd64 |
| github.com/wailsapp/wails/v2 | v2.16.0 | MIT | app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/xdg-go/scram | v1.2.0 | Apache-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/xdg-go/stringprep | v1.0.4 | Apache-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/youmark/pkcs8 | v0.0.0-20240726163527-a2c0da244d78 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/ysmood/fetchup | v0.2.3 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/ysmood/goob | v0.4.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/ysmood/got | v0.40.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/ysmood/gson | v0.7.3 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/ysmood/leakless | v0.9.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| github.com/yuin/goldmark | v1.8.6 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| github.com/zalando/go-keyring | v0.2.8 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| go.mongodb.org/mongo-driver/v2 | v2.9.1 | Apache-2.0 | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| go.uber.org/atomic | v1.11.0 | MIT | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| golang.org/x/crypto | v0.57.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| golang.org/x/image | v0.45.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| golang.org/x/net | v0.59.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| golang.org/x/sync | v0.23.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| golang.org/x/term | v0.46.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| golang.org/x/text | v0.42.0 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64 |
| modernc.org/libc | v1.77.1 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| modernc.org/memory | v1.12.1 | BSD-3-Clause | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause+MIT+Public-Domain | cli darwin/amd64, cli darwin/arm64, cli linux/amd64, cli linux/arm64, cli windows/amd64, app darwin/arm64, app linux/amd64, app windows/amd64 |

## Findings

1. **`github.com/go-sql-driver/mysql` is MPL-2.0 and is linked into the CLI** (the MySQL node, `internal/nodes/db/mysql.go`). MPL-2.0 is a per-file copyleft: a binary that includes it may be distributed under other terms for the rest of the program if recipients are told how to get the source of the MPL-covered files (MPL-2.0 §3.2) and the MPL text travels with the binary. No modified copy is vendored here, so naming the module and version as the source meets that; `NOTICE` does it (`Source code: https://github.com/go-sql-driver/mysql`). Counsel confirms; the alternative is to drop the node.
2. **No other copyleft code ships.** The twelve MPL-2.0 npm packages (`lightningcss` and its platform binaries) are build tooling and are not in the app's files.
3. **Releases ship binaries without third-party license texts today** (`release.yml`, "Flatten and checksum all release files": binaries, archives and checksums only). MIT, BSD, ISC and Apache-2.0 ask for their text to accompany a binary. `NOTICE` is that text; Task 4 attaches it to every release with one `cp` line, a change that can land alone whether or not the repository is ever closed.
4. **`modernc.org/sqlite`** keeps BSD-3-Clause, MIT (sqlite-vec) and the SQLite public-domain dedication in separate files, plus `LICENSE-3RD-PARTY.md` for the C it transpiles; `NOTICE` includes all of them, and the Apache-2.0 modules' `NOTICE` files likewise.
5. **Vendored code:** `internal/jevpick/snapshot.js` (Browser Use, MIT) is in `NOTICE` by hand (`VENDORED` in `scripts/licenseaudit/audit.py`); add any other vendored file the copyright audit finds.
6. **Not covered:** images and fonts under `assets/` and in the desktop frontend, and the monomind runtime (a separate program the app starts, not linked in).
