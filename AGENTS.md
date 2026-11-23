# SIS

Read CLAUDE.md for repository development and verification rules.

## Graphify

For architecture and impact questions spanning SIS modules, start with a scoped Graphify lookup when the local CLI is available.

    powershell -NoProfile -ExecutionPolicy RemoteSigned -File scripts/sis-graphify.ps1 query "VerifyAccount proxy" --depth 2 --budget 1500
    powershell -NoProfile -ExecutionPolicy RemoteSigned -File scripts/sis-graphify.ps1 path VerifyAccount PickForIPs
    powershell -NoProfile -ExecutionPolicy RemoteSigned -File scripts/sis-graphify.ps1 explain KlineHub

Use this launcher for SIS Graphify work, including when invoking the graphify skill. It indexes the committed HEAD from pkg, services, migrations, frontend/src and cmd. Semantic extraction is disabled. It refreshes on demand when HEAD changes. Invoke it without arguments to see the output locations.

Verify relevant links in the source. Graphify can miss runtime links across HTTP, SQL, interfaces and WebSockets. Inspect the working diff for uncommitted changes.
