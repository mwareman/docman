# DocMan repository notes

## Versioning

DocMan stays on **1.x**: 1.9, 1.9.1, 1.9.2, 1.9.3, 1.9.4, then 1.9.5 and so on.
**Do not move to 2.x until the owner says so.**

Each release is built for four architectures and saved to `dist/`:

```bash
docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/386 \
  --build-arg VERSION=1.9.4 -t docman:1.9.4 -t docman:latest --load .
docker save --platform linux/amd64 docman:1.9.4 docman:latest | gzip -9 > dist/docman-1.9.4-amd64.tar.gz
```

Vet and test without a local Go toolchain (`go.mod` requires Go 1.25; the CA
file lets module downloads pass the network's TLS interception, see
`build/certs/README.md` — use your own file's name, or drop the `-e` off such a network; in Git Bash prefix the command with `MSYS_NO_PATHCONV=1`):

```bash
docker run --rm -v "$PWD:/src" -w /src -e SSL_CERT_FILE=/src/build/certs/host-roots.crt \
  golang:1.25-alpine sh -c "go vet ./... && go test ./..."
```

## Release history

| Version | Changes |
| --- | --- |
| 1.9.4 | Faster container and image lists: containers not yet seen are inspected in parallel (up to 8) instead of one after another, update detection shares one image list for 3 seconds, and the Containers page asks over REST only if the live stream has not delivered within 1.5 s. The sidebar counts come from a new light `GET /api/counts` instead of three full lists; the container and image lists are cached for 3 s (cleared by any change made through the API), which the Images page's in-use lookup and the counts share. The Dockerfile builds on `golang:1.25-alpine` so each platform no longer downloads the Go 1.25 toolchain. |
| 1.9.3 | Recreate onto a new image no longer copies the old image's built-in settings: environment entries, labels, command, entrypoint, working directory, user, health check, exposed ports and declared volumes that are still exactly the old image's values are left to the new image (`dock.DropImageDefaults`, applied in `recreateContainer`; unit tests in `internal/dock/inherit_test.go`, and checked end to end against a real engine). Found with TPRM: after recreating onto 1.0.27 its container still carried `TPRM_VERSION=1.0.26` from the old image. The command is only left to the image when the entrypoint is too (Docker ignores an image's `Cmd` once a container has its own entrypoint). Configuration shows Entrypoint and Command empty, with the image's values as placeholders, when they are the image's, and says whether each is the image's or the owner's; `GET …/spec` returns `image_defaults` for it, and the overview tags the command **image default** or **custom**. The documented vet command now uses `golang:1.25-alpine` (`go.mod` requires 1.25) and the host CA file; the Dockerfile build is unaffected — it sets `GOTOOLCHAIN=auto` and fetches 1.25 itself. |
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
NOTICE file carries the attribution derivatives must keep. `dist/` **is**
committed, so others can download the pre-built archives, and DocMan's own
GitHub repository registry (folder `dist`) installs and updates from it. Each
release adds about 45 MB for good; past about 1 GB, move new archives to
GitHub release assets instead. How to use
git with it is in `git.md`, which is kept out of the repository.
