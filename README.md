# GPT Vault

**English** | [简体中文](README.zh-CN.md)

**Your ChatGPT and Codex conversations, archived locally.**

You remember solving that problem with an AI a few weeks ago. But which conversation was it? Was it in Windows, WSL, or ChatGPT? And will you still have it when you move to another machine?

GPT Vault gives those conversations a home: bring them together, find the answer you remember, read its text and images, and keep a complete backup on your own terms.

![GPT Vault showing ChatGPT and Codex conversations together, with a formatted answer open for reading.](docs/images/conversation.png)

*All screenshots use fictional projects, conversations, and paths.*

## One SQLite file. Take your history anywhere.

Your imported conversations, original records, collected images, favorites, and search indexes live in one archive. Make a complete backup, take it to another machine, and pick up where you left off.

- **Easy to back up.** Save a complete snapshot to a folder or download it from the browser. Choose plain SQLite or a compressed `.sqlite.zst` backup.
- **Easy to move.** Restore the backup and point GPT Vault at the restored file. The packaged app runs without Node.js, Python, a database server, or a search service.
- **Your data stays yours.** Conversations stay on your machine. GPT Vault does not upload them, call a model API, or collect telemetry.

Use the app's backup button when it is running: copying only the live `.sqlite` file can miss recent changes in its companion WAL file. Backups include everything already collected in the archive; unreadable attachments and conversations you have never imported are outside it.

## Bring your ChatGPT conversations home, too

You write code in Codex and discuss plans, research, and ideas in Chat. Finding an answer later means switching between them. GPT Vault brings those ChatGPT conversations into the same archive, ready to search, read, favorite, and back up alongside your coding history.

- **Collect everyday chats without repeated exports.** Automatically discover cached ChatGPT conversations from Chat mode in the Windows desktop app. Keep GPT Vault open to sync new content roughly every five minutes.
- **Fill gaps with optional Desktop enhanced sync.** Run `Start ChatGPT Enhanced.cmd`, then enable **桌面同步与历史补全** in Data and sources. GPT Vault checks the 20 most recently updated conversations through Desktop's logged-in page, imports new chats even before their bodies reach the disk cache, and fills older pages in batches. The dedicated launcher restarts Desktop once when needed; automatic sync never restarts it.
- **Add your fuller history.** Import an official ChatGPT export, preserving conversation branches and combining it with already collected content without duplicates.
- **Keep your own copy.** Read collected conversation text offline and take it to another computer with a complete backup.

Default collection covers content cached by the desktop app. Even a finished new chat may not have reached the disk cache, so waiting and syncing again cannot guarantee discovery in this mode. Enhanced sync discovers the 20 most recent non-archived chats as well as supplementing previously collected Desktop conversations, persists pagination progress, and merges by message ID. New and recently updated chats take priority over older history. It does not enumerate every cloud conversation in the account; official exports remain available for account-wide imports.

## Stop switching between Windows and WSL to find a conversation

Codex history can be scattered across your Windows host, WSL users, and archived sessions. GPT Vault brings readable sources into one place.

- Discovers local Codex records, including Windows user folders mounted in WSL.
- Includes both active and archived sessions, with support for older and newer record formats.
- Lets you add source paths yourself, including Windows paths such as `C:\Users\you\.codex` when running in WSL.

Source files are read without being changed. Other WSL distributions must be available through a readable path.

## Find the answer you remember, even when you forget the title

- **Search across everything**, or narrow the results to a project, source platform, app, date range, or favorites.
- **Search Chinese and English**, including one- and two-character Chinese queries and English word fragments.
- **Jump to matches inside a conversation** instead of scrolling through a long history.
- **Keep results useful.** Search conversation text by default; include tool output and context when you need them.

Use several keywords to find text containing all of them, or double quotes for an exact phrase. Press `Ctrl / ⌘ K` for global search and `Ctrl / ⌘ F` to search the open conversation.

![Search across projects with matching text highlighted in results and the conversation, shown in dark mode.](docs/images/search-dark.png)

## Read conversations comfortably

Browse by project, read formatted Markdown, copy code, and follow local file links. Fold away tool calls and context, adjust the text size, or switch between light and dark themes. Long conversations load in manageable sections.

Favorite conversations you want to revisit. Hide projects or conversations to tidy your library, then show them again whenever you like. Hiding keeps their contents and backups intact, and your choices survive later syncs.

You can also export an individual conversation as Markdown or JSON.

Delete a conversation with the trash button beside its visibility button, then confirm. This removes its local content, indexes, and raw records while leaving source files untouched. Incremental sync respects deletions across restarts; a full rescan can reimport deleted conversations from available sources. Projects disappear from navigation when their last conversation is deleted.

## Read ChatGPT's richer answers without losing the useful parts

An answer looked fine in ChatGPT, but its saved copy is missing a paragraph, an image, or half a recommendation card. GPT Vault supports ChatGPT's newer visual answers so those details remain readable when you revisit them.

- **Keep the words and their layout.** Read recommendation cards, columns, tables, diagrams, and source links, including the prose inside cards.
- **Keep track of images.** Image descriptions and source links remain available. Click to load an image; when the original record has none, a note explains what is missing.
- **Recover details in existing conversations.** On startup, GPT Vault restores omitted visual content from saved original records, even when the source is no longer available.

Markdown, code blocks, and ordinary tables keep their familiar presentation. Sliders, forms, and other interactive content appear as static snapshots, with the original content available to inspect. Remote images require an internet connection to load.

## Keep the history, even when the source changes

Incremental sync skips unchanged files and collects new content while you keep reading. If a source file disappears, is truncated, or becomes unavailable, previously imported messages remain in your archive.

Original records and earlier imported event versions are retained. Embedded images and readable local images are stored in the archive; missing attachments keep their references. Remote images are not downloaded automatically.

## Get started

Build from source with **Node.js 22+, pnpm, and Go 1.26+**:

```bash
git clone https://github.com/hafung/gpt-vault.git
cd gpt-vault
pnpm install --frozen-lockfile
pnpm build
pnpm start
```

Open **http://localhost:8765**, then click **同步对话** (Sync conversations). Open **数据与来源** (Data & sources) to add a source, import a ChatGPT export, or manage backups.

After building, Windows users can double-click **Start GPT Vault.cmd** in Explorer instead of running `pnpm start`. It launches projects stored in WSL in the correct distribution, opens the browser, and reuses an existing service. You can create a desktop shortcut and minimize the launcher window. Keeping a page open enables incremental sync roughly every five minutes.

The app currently has a Chinese interface. These READMEs are available in both languages.

After building, you can run the executable directly:

```bash
./bin/gpt-vault
./bin/gpt-vault --port 8765 --data /path/to/echo.sqlite
```

The default archive remains `data/echo.sqlite`, relative to the directory you start from, so existing archives continue to work after the rename. Existing `Start Echo Vault.cmd` shortcuts also launch GPT Vault. The app listens on `127.0.0.1` for personal use on your machine.

## Back up and restore

In **数据与来源** (Data & sources), save a full backup to a folder or download it. Backups contain a consistent snapshot, including committed changes, favorites, hidden items, original records, search indexes, and collected attachments. You can continue reading and syncing during a backup.

![Manage Windows and WSL sources, save a compressed archive backup, and restore to a separate file.](docs/images/backup.png)

Restore a `.sqlite` or `.sqlite.zst` backup from the same panel. GPT Vault verifies it and writes a separate archive, then shows the command to launch it. Your current archive is kept intact.

You can also restore from the command line to a new file:

```bash
./bin/gpt-vault --restore /path/to/backup.sqlite.zst --data /new/path/echo.sqlite
./bin/gpt-vault --data /new/path/echo.sqlite
```

Compression saves transfer and storage space; the savings depend on the content. Backups retain the full archive in either format.

For source compatibility, detailed backup options, development, and maintenance commands, see the [usage guide](docs/guide.md).
