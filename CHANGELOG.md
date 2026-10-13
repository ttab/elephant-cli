# Changelog

All notable changes after v1.0.0 are documented here. Entries are derived from
the release tags; the commits behind each tag hold the detail.

## [v1.6.0] - 2026-10-13

**Breaking:** the `elephant mcp` command is gone. The hosted Elephant MCP
service replaces it, so an assistant configured to start `elephant mcp` has to
be pointed at the hosted service instead.

Changes:

- `elephant configure --well-known`, or a bare `elephant configure` in a
  terminal, sets up `tt-stage`, `tt-prod`, `ntb-stage` and `ntb-prod`, with
  `stage` and `prod` as aliases for the TT ones unless they are already
  configured. `configure --alias` makes any environment an alias, and
  `configure list` shows what is configured. An environment that has never
  been configured is reported with how to configure it, instead of crashing
  with a nil pointer dereference. See [Configuration](README.md#configuration).
- `doc-copy` and `docs-copy` take `--status`, set together with the update,
  and repeatable `--access perms:uri` entries that replace the default
  `redaktionen` ACL.
- `acl-update` merges permissions into the ACL of one document, or of every
  document of a type, and only writes the documents that are missing one.
- The generated service commands cover the latest elephant-api.
- Dependency upgrades: clitools to v1.1.1, elephant-api, elephantine and
  urfave/cli.

## [v1.5.1] - 2026-04-13

- `configure --oidc` accepts an issuer URL and appends
  `/.well-known/openid-configuration` when it is missing.

## [v1.5.0] - 2026-04-10

**Breaking:** service commands are named after their package, as in
`repository.documents`, `spell.check` and `eidos.tagger`, so that services
from different APIs cannot collide. Scripts that call a service command by
its bare name have to use the qualified one.

Changes:

- The TT-specific services from elephant-tt-api have commands: the tagger,
  baboon's assets and print, the NTB metadata, media and nynorsk services,
  everysport's manage and the wires RSS feed.
- `elephant mcp` runs a Model Context Protocol server over stdio or HTTP that
  lets an assistant discover, inspect and call the services, including tools
  for navigating document schemas and index mappings.
- Every service method command takes `--req-schema` and `--res-schema`, which
  print the JSON Schema of the request or response and exit.
- Generated commands and flags carry the `.proto` comments as their help text.

## [v1.0.0] - 2026-04-10

- First stable release.
