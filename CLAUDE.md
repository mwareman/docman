# DocMan — notes for Claude

## Versioning (set by the owner — do not change without being told)

- DocMan stays on **1.x**. Current release: **1.9.2**; the next is 1.9.3, and so on.
- **Never** release 2.0 or any 2.x until the owner explicitly says to.
- A release means: `--build-arg VERSION=<v>`, tags `docman:<v>` and `docman:latest`, archives `dist/docman-<v>-*.tar.gz`, and the version references in `README.md`. Record it in `NOTES.md`.

See `NOTES.md` for the release history.

- The code is published at https://github.com/mwareman/docman (Apache-2.0, with NOTICE). `git.md` holds the owner's git instructions and is ignored by git; keep it up to date when the workflow changes, and never commit `dist/`, data files or `build/certs/*.crt`.

## Working on this repo

- No Go toolchain on the workstation: build and vet inside `golang:1.24-alpine` (see `NOTES.md`), and check JavaScript with `node --check` in `node:22-alpine`. Copy each file to a `.mjs` name first: Node 22 passes a plain `.js` ES module even with syntax errors.
- The workstation's Docker holds the owner's own images and volumes. Remove only what you created, by name. Never run `docker image prune`, `docker volume prune` or DocMan's **Prune** buttons against it.
