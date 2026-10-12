# Echo Vault · 回声

**English** | [简体中文](README.zh-CN.md)

**Your Codex and ChatGPT conversations, together in a local archive you own.**

You remember solving that problem with an AI a few weeks ago. But which conversation was it? Was it in Windows, WSL, or ChatGPT? And will you still have it when you move to another machine?

Echo Vault gives those conversations a home: bring them together, find the useful parts, and keep a complete backup on your own terms.

## One SQLite file. Take your history anywhere.

Your imported conversations, original records, collected images, favorites, and search indexes live in one archive. Make a complete backup, take it to another machine, and pick up where you left off.

- **Easy to back up.** Save a complete snapshot to a folder or download it from the browser. Choose plain SQLite or a compressed `.sqlite.zst` backup.
- **Easy to move.** Restore the backup and point Echo Vault at the restored file. The packaged app runs without Node.js, Python, a database server, or a search service.
- **Your data stays yours.** Conversations stay on your machine. Echo Vault does not upload them, call a model API, or collect telemetry.

Use the app's backup button when it is running: copying only the live `.sqlite` file can miss recent changes in its companion WAL file. Backups include everything already collected in the archive; unreadable attachments and conversations you have never imported are outside it.

## Stop switching between Windows and WSL to find a conversation

Codex history can be scattered across your Windows host, WSL users, and archived sessions. Echo Vault brings readable sources into one place.

- Discovers local Codex records, including Windows user folders mounted in WSL.
- Includes both active and archived sessions, with support for older and newer record formats.
- Lets you add source paths yourself, including Windows paths such as `C:\Users\you\.codex` when running in WSL.
- Imports ChatGPT's `conversations.json` or a ZIP containing it, preserving conversation branches.

Source files are read without being changed. Other WSL distributions must be available through a readable path. ChatGPT history comes from an export you provide; there is no live cloud sync.

## Find the answer you remember, even when you forget the title

- **Search across everything**, or narrow the results to a project, source platform, app, date range, or favorites.
- **Search Chinese and English**, including one- and two-character Chinese queries and English word fragments.
- **Jump to matches inside a conversation** instead of scrolling through a long history.
- **Keep results useful.** Search conversation text by default; include tool output and context when you need them.

Use several keywords to find text containing all of them, or double quotes for an exact phrase. Press `Ctrl / ⌘ K` for global search and `Ctrl / ⌘ F` to search the open conversation.

## Read conversations comfortably

Browse by project, read formatted Markdown, copy code, and follow local file links. Fold away tool calls and context, adjust the text size, or switch between light and dark themes. Long conversations load in manageable sections.

Favorite conversations you want to revisit. Hide projects or conversations to tidy your library, then show them again whenever you like. Hiding keeps their contents and backups intact, and your choices survive later syncs.

You can also export an individual conversation as Markdown or JSON.

Delete a conversation with the trash button beside its visibility button, then confirm. This removes its local content, indexes, and raw records while leaving source files untouched. Incremental sync respects deletions across restarts; a full rescan can reimport deleted conversations from available sources. Projects disappear from navigation when their last conversation is deleted.

## Keep the history, even when the source changes

Incremental sync skips unchanged files and collects new content while you keep reading. If a source file disappears, is truncated, or becomes unavailable, previously imported messages remain in your archive.

Original records and earlier imported event versions are retained. Embedded images and readable local images are stored in the archive; missing attachments keep their references. Remote images are not downloaded automatically.

## Get started

Build from source with **Node.js 22+, pnpm, and Go 1.26+**:

```bash
git clone https://github.com/hafung/echo-vault.git
cd echo-vault
pnpm install --frozen-lockfile
pnpm build
pnpm start
```

Open **http://localhost:8765**, then click **同步对话** (Sync conversations). Open **数据与来源** (Data & sources) to add a source, import a ChatGPT export, or manage backups.

The app currently has a Chinese interface. These READMEs are available in both languages.

After building, you can run the executable directly:

```bash
./bin/echo-vault
./bin/echo-vault --port 8765 --data /path/to/echo.sqlite
```

The default archive is `data/echo.sqlite`, relative to the directory you start from. The app listens on `127.0.0.1` for personal use on your machine.

## Back up and restore

In **数据与来源** (Data & sources), save a full backup to a folder or download it. Backups contain a consistent snapshot, including committed changes, favorites, hidden items, original records, search indexes, and collected attachments. You can continue reading and syncing during a backup.

Restore a `.sqlite` or `.sqlite.zst` backup from the same panel. Echo Vault verifies it and writes a separate archive, then shows the command to launch it. Your current archive is kept intact.

You can also restore from the command line to a new file:

```bash
./bin/echo-vault --restore /path/to/backup.sqlite.zst --data /new/path/echo.sqlite
./bin/echo-vault --data /new/path/echo.sqlite
```

Compression saves transfer and storage space; the savings depend on the content. Backups retain the full archive in either format.

For source compatibility, detailed backup options, development, and maintenance commands, see the [usage guide](docs/guide.md).
