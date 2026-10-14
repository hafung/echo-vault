# Echo Vault usage and maintenance guide

**English** | [简体中文](guide.zh-CN.md) · [Back to README](../README.md)

## Build and run

Building requires Node.js 22+, pnpm, and Go 1.26+:

```bash
pnpm install --frozen-lockfile
pnpm build
pnpm start
```

The frontend is embedded in the executable. Once built, `./bin/echo-vault` runs without the frontend build directory or any development tools. By default, it serves `http://localhost:8765` and stores the archive at `data/echo.sqlite`, relative to the working directory.

On Windows, double-click `Start Echo Vault.cmd` in Explorer, or create a desktop shortcut to it. WSL UNC paths select the corresponding distribution and Linux project directory; ordinary Windows directories use `bin\echo-vault.exe`. Minimize the launcher window to keep the service running. On Linux / WSL, run `bash scripts/start-echo-vault.sh`. The executable's `--open-browser` option opens the browser after startup and reuses a running Echo Vault service. The launchers do not install dependencies, build the app, or configure login startup.

For a new service, the launcher stays open and can be minimized. Launching again opens the existing page and exits the new launcher without stopping the service. Failures pause the window to show the error. CMD may initially report that a WSL UNC current directory is unsupported; the launcher uses absolute script paths and `wsl.exe --cd`, so this does not prevent startup. It clears that initial notice and shows the launcher behavior instead.

```bash
./bin/echo-vault --port 8765 --data /path/to/echo.sqlite
```

For frontend development, start the app, then run `pnpm dev` and open `http://localhost:5178`.

After building the frontend, you can cross-compile a Windows executable:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=local go build -buildvcs=false -trimpath -ldflags="-s -w" -o bin/echo-vault.exe .
```

## Sources and preservation

While a page remains open, an incremental sync starts about five minutes after the previous sync finishes. Opening or returning to the page also checks whether a sync is due. Server-side timing is shared across tabs, and running syncs do not overlap; manual sync remains available. Automatic completion refreshes the list and counts without a success toast. Closing every page prevents further automatic runs; a running sync continues to completion. Browser suspension and computer sleep can delay timers; returning to the page checks for an overdue sync. Frequent collection reduces the chance of losing Desktop Chat cache entries before import, but cannot recover entries already evicted.

- Codex discovery includes the current user's home, `CODEX_HOME`, readable WSL user directories, mounted Windows user directories, and accessible mounted WSL distributions. Additional sources can be configured in the app.
- Both `sessions` and `archived_sessions` are scanned. `session_index.jsonl`, `state_*.sqlite`, and newer `thread_history*.sqlite` files supply metadata and history.
- Older `response_item`, newer `item_completed`, and mixed JSONL formats are supported. Stable IDs and adjacent mirrored content remove duplicate representations while retaining real repeated questions.
- Source files are read only. Source SQLite main and WAL files are copied and checked for changes before SQLite reads the temporary copy. Source databases are never opened by SQLite; source SHM files, authentication files, and browser cookies are not accessed.
- Original JSONL bytes are stored as compressed blocks, shared by content hash. Imported versions are retained. Malformed complete lines are preserved and reported; an unfinished final line is retried on the next sync. Encrypted reasoning data is retained but cannot be decrypted for display.
- Checkpoints, imported content, and raw blocks commit together. Unchanged files are skipped; appended files resume after prefix and checkpoint checks. A full scan rechecks file contents.
- Deleting or truncating a source, losing access to it, or removing nodes from a ChatGPT export does not delete previously imported messages. Updates may change the current view while preserving earlier imported raw event versions.
- Embedded images and readable local images are copied into the archive and deduplicated. Missing attachments retain their references. Remote image URLs are not fetched automatically.
- Windows ChatGPT Desktop Chat response caches are discovered automatically, including Windows profiles mounted in WSL. The Chromium blockfile active index is read without modifying the cache; only ChatGPT conversation response bodies are decoded, supporting Brotli, gzip, deflate and uncompressed JSON. Selected entry and response bytes are checked twice; incomplete or changing entries report a warning and retry on the next sync. You can also add the `…\Default\Cache\Cache_Data` directory manually.
- Both full `mapping` responses and paginated `messages` are supported. Pages merge by stable message ID and messages are ordered by time. Cache entries may expire, be evicted, or contain only recent pages and omit other branches. Open the conversation and scroll back in Desktop before syncing to collect additional cached pages. Imported messages and original response JSON survive cache eviction; titles alone are never treated as conversation bodies. Authentication files and cookies are not read.
- Optional Desktop enhanced sync supports Windows and WSL and is off by default. Run `Start ChatGPT Enhanced.cmd`, then enable **桌面主动补全** in Data and sources. The dedicated launcher restarts Desktop only when needed and reuses an existing enhanced instance. Quitting Desktop and launching its normal shortcut returns it to normal mode. Routine sync never starts, closes, navigates, or restarts Desktop.
- The helper connects only to the installed ChatGPT Desktop's CDP endpoint on `127.0.0.1:19222`, checks the listener owner and loopback binding, and discovers the request module in the current installation. It invokes the logged-in page's read-only pagination requests without exporting cookies or login tokens. It does not use the full snapshot endpoint that returned HTTP 403. This depends on Desktop internals; an incompatible update, signed-out page, or closed endpoint produces a warning while normal cache imports continue.
- Enhanced sync supplements previously collected Windows Desktop Chat conversations. Each run processes up to eight conversations, up to twelve pages per conversation, with a shared read budget of about ninety seconds. Unfinished histories persist a resume cursor. Data commits before its checkpoint, so interruption may cause an idempotent reread but never skips uncommitted text. Unchanged completed histories are skipped and rechecked roughly daily; failed conversations retry after thirty minutes. Full scans also use batches. Deleting a conversation during a network read remains effective; incremental capture does not restore it.
- Official ChatGPT imports accept `conversations.json` or a ZIP containing it. All exported branches are retained; non-current branches are marked. Exports merge with cache imports and enhanced sync by conversation and message ID. Content that was not exported or cached and is not accessible to the current account cannot be recovered from a local index.

When running in WSL, Windows paths such as `C:\Users\you\.codex` and `D:\exports\conversations.json` are converted to mounted paths. Other WSL distributions must be made readable first.

## Search and reading

Search is global until a project is selected. Selecting the same project again clears that scope. Filters include source platform, app, favorites, and dates; results can be ordered newest or oldest first.

Whitespace-separated keywords use AND matching. Double quotes group an exact phrase. Input is treated as literal text, not SQL or a user-supplied search expression. Three or more characters use a trigram index; single and double characters use separate indexes. Conversation text is searched by default, with optional tool and context search. Long binary or Base64 blocks are retained without being indexed.

The trash button beside conversation visibility opens a confirmation dialog. Deleting removes local content, indexes, raw records, and exclusive attachments, preserving shared payloads and original sources. No project deletion is provided; empty projects disappear automatically. Persistent deletion markers are included in full backups and prevent incremental reimports, including renamed or mirrored sources with the same session ID. A full scan clears a marker when it reimports that session; unavailable sources keep their markers.

Projects and conversations can be hidden and restored. The eye button beside the project heading reveals hidden and source-archived records. Hiding retains contents, attachments, indexes, and raw records. Local visibility choices survive syncs, full scans, and source archive changes. A hidden project also hides subsequently imported conversations in that project. Full backups always include hidden data.

Messages load in batches of 30, with at most 90 in the continuous reading window. Long messages initially show 24,000 characters and can be expanded. Images load lazily. Font size, theme, and desktop navigation collapse preferences are remembered locally.

Markdown is sanitized. Scripts and inline styles are removed, and remote images are not loaded automatically. Local links resolve relative to the conversation's project directory where appropriate. Opening a file or folder requires confirmation; unavailable paths show a message. WSL can open files with Windows default applications. Programs and scripts can be revealed in their folder.

ChatGPT Intelligent UI (DIL) supports common layouts, prose, entities, citations, tables, and static data lists. Image descriptions and source links are retained; click the image button to load a remote image. On startup, existing archives recover component data from their saved original records. Controls display a static snapshot; complex expressions and apps remain available under the original-content disclosure. Model-written JavaScript is never executed. Ordinary Markdown and code examples keep their existing rendering.

Shortcuts: `Ctrl / ⌘ K` for global search, `Ctrl / ⌘ F` for conversation search, `↑ ↓` to move between conversations, and `Esc` to dismiss panels or clear search.

## Complete backups

Both backup actions in Data & sources use the SQLite online backup API. The result is a consistent snapshot containing committed WAL changes, imported messages, favorites, project and conversation visibility, search indexes, raw blocks, and collected attachments. Unimported sources, unreadable attachments, and unexported cloud conversations are outside the archive.

Formats are fast Zstandard compression (the default), smaller Zstandard compression, and uncompressed SQLite. Compressed files use the standard `.sqlite.zst` format and restore to the same SQLite snapshot bytes. Compression streams data rather than loading the whole archive into memory. Already compressed text, logs, and images may limit the space savings.

Compression first creates an uncompressed snapshot. The destination must have room for that snapshot and the compressed output together. Temporary files are removed after success or failure.

- **Save to a folder:** the app writes directly to the chosen directory, remembers its location, and uses a unique filename for each backup. Windows paths are supported in WSL. Unfinished output has a `.partial` suffix and is renamed when complete.
- **Download:** the app generates a snapshot in the background and streams it through the browser. Download files remain available for one hour; the next backup replaces the previous temporary download. Restarting the service requires generating a new download.

Only one UI backup task runs at a time. Progress shows copying and compression separately. Refreshing or closing the panel does not cancel the job; stopping the service interrupts it. The snapshot remains fixed while new data is imported, so concurrent syncing does not force repeated copying. Archive size and available disk space determine the practical limits; there is no project or conversation count cap.

Use these actions rather than copying only a running database's main `.sqlite` file, which can miss committed data in the WAL.

## Restore and migrate

Data & sources accepts a local `.sqlite` / `.sqlite.zst` path or an uploaded backup. Local paths are preferable for large archives. Restore writes an independent SQLite file into the `restored` directory beside the current database and checks the archive schema and SQLite `quick_check` result. The panel then shows the new path and a launch command.

Stop the current service and run that command to switch archives. Restore neither overwrites the current file nor merges archives. You can read while background restore and validation run; refreshing the panel retains job status. Stopping the service interrupts an unfinished restore.

Backup uploads have a 1 TB limit; JSON / ZIP source imports have a separate 512 MB limit.

Offline restore requires a destination that does not already exist:

```bash
./bin/echo-vault --restore /path/to/backup.sqlite.zst --data /new/path/echo.sqlite
./bin/echo-vault --data /new/path/echo.sqlite
```

An uncompressed SQLite backup can also be opened directly with `--data`. If it has an associated nonempty WAL, make an online backup first to retain committed changes. On a new machine, source paths may need updating; already imported history remains readable. Files in `data/imports` are retained for source import retries, while successfully imported content lives in the database.

## Maintenance

Stop the running service before using commands that write to the same archive. Add `--data /path/to/echo.sqlite` when working with a nondefault archive.

```bash
./bin/echo-vault --sync-only         # Incremental sync, then exit
./bin/echo-vault --sync-only --full  # Rescan sources, keeping imported history
./bin/echo-vault --reindex           # Rebuild all derived search indexes
./bin/echo-vault --index-dialogue    # Rebuild conversation-text indexes only
./bin/echo-vault --deduplicate       # Deduplicate raw blocks, keeping all versions
./bin/echo-vault --compact           # Reclaim storage with SQLite VACUUM
```

The implementation uses Go, SQLite with WAL and FTS5, and a TypeScript frontend. The SQLite driver does not require CGO.

The service binds to `127.0.0.1`, validates Host, and checks same-origin requests and a custom header for writes. It has no remote account system. On Linux / WSL, new archive directories use `0700` permissions and database files use `0600`.

## Checks

```bash
pnpm build       # TypeScript check, frontend build, and server build
pnpm test        # Go tests for importing, search, preservation, backup, and restore
pnpm test:links  # Local-path recognition and Markdown-link tests
pnpm test:e2e    # Chromium interaction tests
```

Build the frontend before running Go tests because the server embeds `dist`. Browser tests require a running app and Playwright Chromium. Some browser tests expect populated conversation data, including Windows and WSL records; others use mocked responses. Run them against a disposable test archive. `ECHO_TEST_URL` sets the server URL; `ECHO_CHROMIUM` sets the browser executable. Screenshots go to `artifacts/`.

Personal archives, attachments, builds, and test output are excluded from version control.
