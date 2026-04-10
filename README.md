# elephant-cli

CLI tool for the Elephant content management system. Provides both hand-crafted
convenience commands and auto-generated wrappers for all elephant-api Twirp
services.

## Installation

```sh
go install github.com/ttab/elephant-cli/cmd/elephant@latest
```

## Configuration

Before using the CLI, configure the environment with the OIDC provider and base
URL:

```sh
elephant --env prod configure --oidc https://auth.example.com/realms/elephant
elephant --env prod configure --base-url https://example.com
```

Individual endpoints can be overridden:

```sh
elephant --env prod configure --endpoint repository=https://custom-repo.example.com
```

On first use the CLI opens a browser for OAuth2 login (PKCE flow) and caches
tokens locally under `~/.config/elephant-clitools/<env>/`.

## Global flags

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--env` | `ENV` | `stage` | Target environment |
| `--scopes` | `ELEPHANT_SCOPES` | `doc_read,eventlog_read,metrics_read,schema_read,search,user` | OAuth2 scopes to request |
| `--fmt-toon` | | `false` | Use toon format instead of JSON |

### Scopes

The default scopes are read-only. Override them to enable write or admin
operations:

```sh
elephant --scopes doc_read --scopes doc_write --scopes search documents update --file doc.json
```

Or via the environment variable:

```sh
export ELEPHANT_SCOPES="doc_read,doc_write,search"
```

## Convenience commands

These are hand-written commands with tailored workflows:

| Command | Description |
|---------|-------------|
| `doc-download` | Download a document by UUID or URI |
| `doc-upload` | Upload/update a document from stdin |
| `doc-versions` | List document version history |
| `doc-copy` | Copy a document between environments |
| `docs-copy` | Bulk copy documents by type between environments |
| `docs-list` | List documents by type |
| `search` | Query the index with a JSON query from stdin |
| `set-status` | Update document status |
| `run-list` | Batch process documents from a CSV file |

## Generated service commands

Auto-generated wrappers cover every RPC method in the elephant-api. Each service
is a top-level command with methods as subcommands:

```
elephant <service> <method> [flags]
```

### Available services

| Command | Service | Endpoint |
|---------|---------|----------|
| `documents` | repository.Documents | repository |
| `schemas` | repository.Schemas | repository |
| `workflows` | repository.Workflows | repository |
| `metrics` | repository.Metrics | repository |
| `management` | index.Management | index |
| `search-v1` | index.SearchV1 | index |
| `settings` | user.Settings | user |
| `messages` | user.Messages | user |
| `check` | spell.Check | spell |
| `dictionaries` | spell.Dictionaries | spell |
| `replication` | replicant.Replication | replicant |

### Using flags

Methods whose request messages contain only scalar fields can be invoked
directly with flags:

```sh
elephant documents get --uuid 7a3f08c1-b252-4b4a-af3e-b9301ed3b05a
elephant documents get-history --uuid 7a3f08c1-b252-4b4a-af3e-b9301ed3b05a --before 5
elephant schemas list-active
elephant dictionaries list-entries --language sv --page 1
```

### Using JSON input

All methods accept `--file` (`-f`) to read the request as JSON from a file or
stdin:

```sh
elephant documents update --file request.json
echo '{"uuid":"..."}' | elephant documents get --file -
```

### Using toon format

Pass `--fmt-toon` to read and write
[toon](https://github.com/toon-format/toon-go) instead of JSON:

```sh
elephant --fmt-toon documents get --uuid 7a3f08c1-b252-4b4a-af3e-b9301ed3b05a
```

### JSON Schemas

Every method command supports `--req-schema` and `--res-schema` to print the
JSON Schema for the request and response messages:

```sh
elephant documents get --req-schema
elephant documents get --res-schema
```

## MCP server

The `mcp` command runs a [Model Context Protocol](https://modelcontextprotocol.io/)
server that exposes the elephant-api to AI assistants like Claude. The `--env`
and `--scopes` flags control which environment and permissions the assistant gets.

### Tools exposed

**Core tools:**

| Tool | Description |
|------|-------------|
| `elephant_list_services` | List all services and methods for discovery |
| `elephant_search_methods` | Search methods by keyword in names and descriptions |
| `elephant_get_schema` | Get the JSON Schema for a method's request or response |
| `elephant_call` | Invoke an API method with a JSON payload |

**Schema and index tools:**

| Tool | Description |
|------|-------------|
| `elephant_schema_overview` | List active validation schemas with declared/extended document types |
| `elephant_type_constraints` | Get allowed meta, content, and link blocks for a document type |
| `elephant_resolve_ref` | Look up a reusable block definition by its ref URI |
| `elephant_enum_values` | Get allowed values for an enum reference |
| `elephant_index_mappings` | Search index field mappings for a document type, with optional name filter |

### Registering with Claude Code (stdio)

Register the MCP server as a stdio process. The `--env` and `--scopes` flags
are passed directly to control access:

```sh
# Read-only access to production (default scopes)
claude mcp add --scope user elephant-prod -- elephant --env prod mcp

# Read-write access to staging
claude mcp add --scope user elephant-stage -- \
  elephant --env stage --scopes doc_read --scopes doc_write --scopes search mcp
```

### Registering with Claude Code (HTTP)

Run the MCP server as a shared HTTP service, then point Claude at it:

```sh
# Start the server (in a terminal or as a service)
elephant --env stage mcp --http :8080

# Register the HTTP endpoint
claude mcp add --scope user --transport http elephant-stage http://localhost:8080/
```

### Project-specific `.mcp.json`

Add an `.mcp.json` file to a project root to automatically configure the MCP
server for that project. This is useful for giving Claude project-specific
access with appropriate scopes:

```json
{
  "mcpServers": {
    "elephant-prod": {
      "command": "elephant",
      "args": ["--env", "prod", "--scopes", "doc_read", "--scopes", "search", "mcp"]
    }
  }
}
```

For read-write access:

```json
{
  "mcpServers": {
    "elephant-stage": {
      "command": "elephant",
      "args": [
        "--env", "stage",
        "--scopes", "doc_read",
        "--scopes", "doc_write",
        "--scopes", "schema_read",
        "--scopes", "search",
        "mcp"
      ]
    }
  }
}
```

For an HTTP server:

```json
{
  "mcpServers": {
    "elephant-stage": {
      "type": "http",
      "url": "http://localhost:8080/"
    }
  }
}
```

### Access control

The scopes you pass determine what the AI assistant can do:

| Scope | Grants |
|-------|--------|
| `doc_read` | Read documents |
| `doc_write` | Create and update documents |
| `doc_delete` | Delete documents |
| `schema_read` | Read schemas |
| `schema_admin` | Manage schemas |
| `eventlog_read` | Read the event log |
| `metrics_read` | Read metrics |
| `search` | Search the index |
| `user` | Access user settings and messages |
| `spell_write` | Manage spell check dictionaries |
| `workflow_admin` | Manage workflows |
| `index_admin` | Manage index clusters and sets |

The default scopes (`doc_read`, `eventlog_read`, `metrics_read`, `schema_read`,
`search`, `user`) provide read-only access. This is the safest starting point
for AI assistants.

## Code generation

The generated service commands are produced by `cmd/generate-cli`, which uses
protobuf reflection to enumerate all services and methods from the elephant-api
descriptors. Regenerate after updating the elephant-api dependency:

```sh
go generate ./internal/apicli/
```
