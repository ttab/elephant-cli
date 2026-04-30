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
elephant --env prod configure \
    --base-url https://example.com \
    --oidc https://auth.example.com/realms/elephant
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
| `repository.documents` | Documents | repository |
| `repository.schemas` | Schemas | repository |
| `repository.workflows` | Workflows | repository |
| `repository.metrics` | Metrics | repository |
| `index.management` | Management | index |
| `index.search-v1` | SearchV1 | index |
| `user.settings` | Settings | user |
| `user.messages` | Messages | user |
| `spell.check` | Check | spell |
| `spell.dictionaries` | Dictionaries | spell |
| `replicant.replication` | Replication | replicant |
| `eidos.tagger` | Tagger | eidos2 |
| `baboon.assets` | Assets | baboon |
| `baboon.print` | Print | baboon |
| `everysport.manage` | Manage | everysport |
| `ntb.metadata` | Metadata | ntb |
| `ntb.media` | Media | ntb |
| `ntb.nynorsk` | Nynorsk | ntb |
| `wires.rss-feed` | RssFeed | wires |

### Using flags

Methods whose request messages contain only scalar fields can be invoked
directly with flags:

```sh
elephant repository.documents get --uuid 7a3f08c1-b252-4b4a-af3e-b9301ed3b05a
elephant repository.documents get-history --uuid 7a3f08c1-b252-4b4a-af3e-b9301ed3b05a --before 5
elephant repository.schemas list-active
elephant spell.dictionaries list-entries --language sv-se --page 1
```

### Using JSON input

All methods accept `--file` (`-f`) to read the request as JSON from a file or
stdin:

```sh
elephant repository.documents update --file request.json
echo '{"uuid":"..."}' | elephant repository.documents get --file -
```

### Using toon format

Pass `--fmt-toon` to read and write
[toon](https://github.com/toon-format/toon-go) instead of JSON:

```sh
elephant --fmt-toon repository.documents get --uuid 7a3f08c1-b252-4b4a-af3e-b9301ed3b05a
```

### JSON Schemas

Every method command supports `--req-schema` and `--res-schema` to print the
JSON Schema for the request and response messages:

```sh
elephant repository.documents get --req-schema
elephant repository.documents get --res-schema
```

## Code generation

The generated service commands are produced by `cmd/generate-cli`, which uses
protobuf reflection to enumerate all services and methods from the elephant-api
descriptors. Regenerate after updating the elephant-api dependency:

```sh
go generate ./internal/apicli/
```
