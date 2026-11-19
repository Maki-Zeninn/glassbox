# Config Show Command

The `config show` command displays the effective runtime configuration as read from flags, environment variables, and config files.

## Usage

```bash
# Human-readable output
glassbox config show

# JSON output with source annotations
glassbox config show --json
```

## Output

### Human-Readable Format

```
Active configuration:
{
  "rpc_url": "https://...",
  "network": "testnet",
  ...
}

Loaded from: /home/user/.glassbox/config.toml

Configuration sources:
  ✓ Environment variables (active)
  ✓ User config (active)
```

### JSON Format

```json
{
  "values": {
    "rpc_url": {
      "value": "https://test.example.com",
      "source": "environment"
    },
    "network": {
      "value": "testnet",
      "source": "file"
    }
  },
  "source_file": "/home/user/.glassbox/config.toml"
}
```

## Configuration Precedence

For a setting supplied by more than one source, the tested order is (highest
wins):

1. An explicit command-line value, when that command accepts a flag for the setting
2. Environment variables
3. Config files
4. Built-in defaults

The tests also verify that project-local values override home-directory values,
and that the XDG-style file overrides the legacy filename in the same
directory. Files are merged by key; finding a project file does not discard
settings from a lower-priority file. Environment variables override file
values. The CLI flag-over-environment case is tested for RPC tokens.

## Config File Locations

The loader reads these user and project files, merging later/higher-priority
values over earlier ones:

- `~/.Glassbox.toml` (home directory, legacy)
- `~/.glassbox/config.toml` (home directory, XDG-style)
- `.Glassbox.toml` (current directory, legacy)
- `.glassbox.toml` (current directory, XDG-style)

The loader also reads `/etc/Glassbox/config.toml`; current precedence tests do
not establish that file's position relative to the user and project files.

## Source Annotations

In JSON mode, each config value includes a `source` field:

| Source | Description |
|--------|-------------|
| `environment` | Set via `GLASSBOX_*` environment variable |
| `file` | Set in a config file |
| `default` | Built-in default value |

For a safe per-field inspection, use the explain report. Its tested redaction
replaces sensitive effective values with `[redacted]`; the selection below also
keeps the output focused on the value, source, and redaction status:

```bash
glassbox config show --explain --json \
  | jq '{active_file, fields: [.fields[] | {field, source, effective_value, redacted, default_applied}]}'
```

The environment bindings and their supported variable names are documented in
[Bindings and environment variables](bindings-environments.md).