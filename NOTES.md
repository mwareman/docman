# DocMan repository notes

## Versioning

DocMan stays on **1.x**: 1.9, 1.9.1, 1.9.2, then 1.9.3 and so on.
**Do not move to 2.x until the owner says so.**

Each release is built for four architectures and saved to `dist/`:

```bash
docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/386 \
  --build-arg VERSION=1.9.2 -t docman:1.9.2 -t docman:latest --load .
docker save --platform linux/amd64 docman:1.9.2 docman:latest | gzip -9 > dist/docman-1.9.2-amd64.tar.gz
```

Vet without a local Go toolchain:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.24-alpine go vet ./...
```

## Release history

| Version | Changes |
| --- | --- |
| 1.9.2 | GitHub repository registries: install and update pre-built image archives from a repository's files or releases (never built from a Dockerfile); Pull, Recreate and the daily check follow the repository; tags named after a version are reported as pinned. The deploy plan keeps the tag that was chosen. Apache-2.0 license with NOTICE. |
| 1.9.1 | Registries in Settings: several registries (Docker Hub, GitHub, GitHub Enterprise, GitLab, Quay, AWS ECR, Google, Azure, any other), each with its own sign-in; choosing and searching a registry when creating a container; stored credentials used for every pull and update check; tag lists paged and sorted newest first. The volume explorer's helper container runs only while the explorer is open. |
| 1.9 | Uploads in pieces that pass any proxy's size limit; upload dialog with two-stage progress and a lock while uploading; fixed addresses rebuilt from Docker when DocMan's database has lost them. |
| 1.8.2 | No empty volumes left by the volume explorer; empty unused anonymous volumes cleaned up; anonymous volumes kept across a recreate (data-loss fix); JSON laid out for editing. |
| 1.8.1 | Fixed deleting volumes, and the volume explorer's file delete (which could delete the whole volume in 1.8 — do not use 1.8). |
| 1.8 | Volume explorer; Inspect; recreates and configuration changes run as server jobs; containers keep their state; old IDs redirect; image in-use tooltips. |
| 1.7 | Daily registry update checks; Pull and Refresh on Images; recreate pulls first for registry images; volume driver list; volume picker for storage. |
| 1.6 | Capability checklist; network dropdown; Update available badge. |
| 1.5 | Multiple user accounts; system-wide API tokens with their creator recorded. |
| 1.4 / 1.4.1 | Built-in help at /help, with the API reference. |
| 1.3 | Upload image without creating a container. |
| 1.2 | UI follows a container's new ID after a recreate. |
| 1.1 | DocMan can recreate itself through a helper container. |
| 1.0 | First release. |

## Repository

Public at https://github.com/mwareman/docman under the Apache License 2.0; the
NOTICE file carries the attribution derivatives must keep. `dist/` is not
committed: each version's archives are attached to its GitHub release, which
is also where DocMan's own GitHub repository registry finds them. How to use
git with it is in `git.md`, which is kept out of the repository.
