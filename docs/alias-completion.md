# Shell Completion

Glassbox generates native completion scripts for Bash, Zsh, Fish, and
PowerShell from the live command tree.  Every public command, subcommand,
flag, and enum-valued option is covered.

---

## Installation

### Bash

```bash
# System-wide (requires root / sudo)
glassbox completion bash | sudo tee /etc/bash_completion.d/glassbox

# Per-user
mkdir -p ~/.local/share/bash-completion/completions
glassbox completion bash > ~/.local/share/bash-completion/completions/glassbox
source ~/.bashrc
```

### Zsh

```zsh
# Add to a directory already on $fpath
glassbox completion zsh > "${fpath[1]}/_glassbox"

# Or create a dedicated completions directory
mkdir -p ~/.zsh/completions
glassbox completion zsh > ~/.zsh/completions/_glassbox

# Add to ~/.zshrc (if not already present)
echo 'fpath=(~/.zsh/completions $fpath)' >> ~/.zshrc
echo 'autoload -Uz compinit && compinit' >> ~/.zshrc
source ~/.zshrc
```

### Fish

```fish
glassbox completion fish > ~/.config/fish/completions/glassbox.fish
# Completions are picked up automatically on the next shell start.
```

### PowerShell

```powershell
# Load for the current session only
glassbox completion powershell | Out-String | Invoke-Expression

# Load for every new session (append to your profile)
glassbox completion powershell >> $PROFILE
```

---

## What is completed

### Commands

Every public command in the tree is reachable through completion, including
all subcommands under `session`, `protocol:*`, `offline`, `snapshot`, and
`audit:*`.

### Enum-valued flags

The following flags suggest only their accepted values — file-system
browsing is suppressed for these:

| Flag | Accepted values | Commands |
|---|---|---|
| `--network` | `testnet`, `mainnet`, `futurenet` (+ any saved custom networks) | `debug`, `compare`, `search`, `generate-bindings`, `check-bindings`, `offline generate`, … |
| `--network` (init) | adds `standalone` | `init` |
| `--theme` | `default`, `dark`, `light`, `deuteranopia`, `protanopia`, `tritanopia`, `high-contrast` | `debug`, `compare`, `trace` |
| `--format` / `--export-format` | `html`, `markdown`, `json`, `text` | `trace` |
| `--format` | `text`, `json` | `debug`, `export`, `generate-bindings` |
| `--format` | `text`, `json`, `html`, `pdf`, `html,pdf` | `report` |
| `--trace-verbosity` | `summary`, `normal`, `verbose` | `debug`, `trace` |
| `--view` | `trace`, `flamegraph`, `events`, `auth`, `budget`, `storage` | `debug` |
| `--log-level` | `trace`, `debug`, `info`, `warn`, `error` | all (root persistent flag) |
| `--profile-format` | `html`, `svg` | all (root persistent flag) |
| `--runtime` | `node`, `browser`, `universal` | `generate-bindings`, `check-bindings` |
| `--spec-format` | `json`, `xdr` | `generate-bindings`, `check-bindings` |
| `--type` | `ledger-entry`, `diagnostic-event` | `xdr` |
| `--failover-strategy` | `weighted`, `sticky`, `round_robin` | (config-level) |

### Command aliases

The following Cobra aliases are registered on their primary commands and
therefore appear in completion alongside the canonical names:

| Alias | Primary command |
|---|---|
| `db` | `debug` |
| `ps` | `profile` |
| `pb:register` | `protocol:register` |
| `pb:unregister` | `protocol:unregister` |
| `pb:status` | `protocol:status` |
| `pb:verify` | `protocol:verify` |
| `pb:handle` | `protocol:handle` |
| `pb:diagnose` | `protocol:diagnose` |
| `pb:repair` | `protocol:repair` |

---

## Guarantees

- **No network calls during completion.**  All completion functions return
  static in-process values.  The only I/O is a local config file read in
  `--network` completion (for custom network names), which is non-blocking
  and falls back cleanly on error.
- **Enum flags never show filenames.**  Every enum-valued flag uses
  `cobra.ShellCompDirectiveNoFileComp` so the shell does not mix filenames
  into the suggestions.

---

## Regenerating snapshots

Completion scripts are golden-file tested.  CI fails when the generated
output changes without the snapshots being updated.

```bash
# Linux / macOS
./scripts/gen-completion-snapshots.sh

# Windows
.\scripts\gen-completion-snapshots.ps1

# Or directly via go test
go test ./internal/cmd/... -run TestCompletionSnapshot -update -count=1
```

Commit the updated files under `internal/cmd/testdata/completion/`.

### Contributor workflow

Regenerate snapshots when an intentional change to the public command tree,
flags, aliases, or completion output changes a generated script. From the
repository root, run the supported generator:

```bash
./scripts/gen-completion-snapshots.sh
```

Review the generated diffs before committing:

```bash
git diff -- internal/cmd/testdata/completion/
```

The diff should correspond to the intentional CLI change. If it shows unrelated
commands, flags, or shell-script changes, investigate the command tree or
completion generator rather than accepting the snapshots blindly. The script
writes only the checked-in completion snapshots; it does not install shell
completions on the local machine. To check existing snapshots without updating
them, run `go test ./internal/cmd/... -run 'TestCompletionSnapshot' -count=1`.

---

## Adding a new enum-valued flag

1. Add the accepted values to the relevant `XxxValues` slice in
   `internal/cmd/completion_helpers.go`.
2. Add a `completeXxxFlag` function in the same file.
3. Call `_ = yourCmd.RegisterFlagCompletionFunc("flag-name", completeXxxFlag)`
   in the command's `init()` block.
4. Add the flag name to `knownEnumFlags` in
   `internal/cmd/completion_snapshot_test.go`.
5. Regenerate snapshots (see above).

The test `TestPublicFlagsHaveCompletion` will fail in CI if step 3 or 4 is
skipped, catching the gap before it ships.

---

## Troubleshooting

This section covers the most common issues users encounter with aliases and
shell completions.  The two features are independent: **aliases** are
Cobra-level shortcuts that the binary resolves at runtime, while
**completions** are shell-level scripts that the shell reads when you press
Tab.  Stale shell configuration is the most frequent root cause of both
appearing broken.

### Aliases vs. completions — what each one does

| Concept | Where it lives | Who resolves it | Example |
|---|---|---|---|
| **Command alias** | Inside the `glassbox` binary (Cobra `Aliases` field) | `glassbox` at runtime | `glassbox db` → runs `glassbox debug` |
| **Shell completion** | A script written to disk by `glassbox completion <shell>` | Your shell (Bash, Zsh, Fish, PowerShell) | Pressing Tab after `glassbox d` → suggests `debug`, `db`, `doctor`, `dry-run` |

Because aliases are resolved by the binary itself, they work immediately
after upgrading `glassbox` — no shell restart or script regeneration is
needed.  Completions, on the other hand, are static scripts on disk; they
must be regenerated and the shell must be restarted (or the script
re-sourced) before changes take effect.

### Checking which aliases are registered

Run the following to inspect the canonical list of aliases at any time
without modifying any file:

```bash
glassbox --help
```

Each command group in the help output lists its commands.  Cobra-registered
aliases (`db`, `ps`, `pb:*`) appear in the `Aliases:` field of the
individual command's help page:

```bash
glassbox debug --help   # shows Aliases: db
glassbox profile --help # shows Aliases: ps
```

### Verifying your completion script is up to date

Inspect what the binary currently generates and compare it against what is
on disk.  Do **not** edit the generated file by hand — always regenerate it
using the supported command.

**Bash**

```bash
# See what the binary generates right now
glassbox completion bash

# Regenerate and install (per-user)
glassbox completion bash > ~/.local/share/bash-completion/completions/glassbox

# Reload without opening a new shell
source ~/.local/share/bash-completion/completions/glassbox
```

**Zsh**

```zsh
# Regenerate
glassbox completion zsh > ~/.zsh/completions/_glassbox

# Reload
autoload -Uz compinit && compinit -C
source ~/.zshrc
```

**Fish**

```fish
# Regenerate (Fish picks up the new file on the next command)
glassbox completion fish > ~/.config/fish/completions/glassbox.fish
```

**PowerShell**

```powershell
# Regenerate for the current session
glassbox completion powershell | Out-String | Invoke-Expression

# Regenerate persistently (overwrites the previous entry in $PROFILE)
# First remove the old entry, then append the fresh output:
glassbox completion powershell >> $PROFILE
. $PROFILE
```

### Aliases not appearing in Tab completion

Cobra-registered aliases (`db`, `ps`, `pb:*`) are included automatically in
every completion script that `glassbox completion <shell>` generates.  If
they do not appear when you press Tab, the completion script on disk is
stale.  Regenerate it using the commands in the section above.

To confirm whether an alias is present in the current script before
regenerating:

```bash
# Bash — search the installed completion file
grep -w "db\|ps\|pb:" ~/.local/share/bash-completion/completions/glassbox

# Zsh
grep -w "db\|ps\|pb:" ~/.zsh/completions/_glassbox

# Fish
grep "db\|ps\|pb:" ~/.config/fish/completions/glassbox.fish
```

If the grep returns no output, the file is outdated.  Regenerate using the
`glassbox completion <shell>` command and reload your shell.

### Completion not working at all after installation

Work through this checklist in order:

1. **Confirm the binary is on `$PATH`.**

   ```bash
   which glassbox          # Linux / macOS / Bash / Zsh
   Get-Command glassbox    # PowerShell
   ```

2. **Confirm the completion script exists and is non-empty.**

   ```bash
   # Bash
   ls -lh ~/.local/share/bash-completion/completions/glassbox

   # Zsh
   ls -lh ~/.zsh/completions/_glassbox

   # Fish
   ls -lh ~/.config/fish/completions/glassbox.fish
   ```

3. **Reload your shell configuration without opening a new terminal.**

   ```bash
   # Bash
   source ~/.bashrc

   # Zsh
   source ~/.zshrc

   # Fish
   source ~/.config/fish/config.fish

   # PowerShell
   . $PROFILE
   ```

4. **For Zsh, confirm `compinit` runs after `fpath` is set.**

   In `~/.zshrc` the `fpath` line must appear *before* the `compinit` line:

   ```zsh
   fpath=(~/.zsh/completions $fpath)
   autoload -Uz compinit && compinit
   ```

5. **Regenerate a fresh script.**  Even if the file exists, it may have been
   generated by an older binary version.  Always use the live binary to
   regenerate:

   ```bash
   glassbox completion bash   # or zsh / fish / powershell
   ```

### Shell configuration is stale after upgrading glassbox

After upgrading the binary, the completion script on disk still describes
the old command tree.  New commands, subcommands, and flags will not be
suggested until the script is regenerated.

**Never edit the generated file directly.**  Re-run the supported install
command for your shell (see the [Installation](#installation) section above)
and reload your shell.

To verify the installed script matches the current binary output:

```bash
# Bash — diff the installed file against fresh output
diff ~/.local/share/bash-completion/completions/glassbox \
     <(glassbox completion bash)

# Zsh
diff ~/.zsh/completions/_glassbox \
     <(glassbox completion zsh)
```

A non-empty diff means the file is stale.  Overwrite it with the output of
`glassbox completion <shell>` and reload.

### Getting help

Run the built-in help for the `completion` command to see the supported
shells and quick-start instructions at any time:

```bash
glassbox completion --help
```
