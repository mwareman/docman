# DocMan

Container management for **one** docker host — the host DocMan itself runs on.
Start it, read a key off the console, pick a password, then harden sign-in the
way you prefer: two passkeys, or an authenticator app that turns the password
into two-step. Everything the UI can do, the REST API can do too. The running
container serves a complete, searchable user guide and API reference at
`/help`, linked from the app's menu and readable without signing in.

One static Go binary with the UI embedded, running on `scratch` — a 17 MB image.
No agents, no remote endpoints, no stacks, no templates, no in-browser image
builds.

## What it does

- **Containers** — start, stop, restart, pause, kill, rename, remove; live logs
  with search and filtering; live CPU / memory / disk / network graphs; an
  interactive console; and a full configuration editor for environment,
  command, ports, storage, limits and capabilities.
- **Fixed internal addresses** — one click gives a container an internal IP that
  never changes, including across recreates. DocMan provisions a bridge network
  with a free private subnet the first time you ask, splits it into an address
  range it allocates from and one it leaves to docker, and moves the container
  over. You never see any of that.
- **Images** — list, inspect layers and defaults, pull (with registry
  credentials), tag, remove, prune, and **upload a `docker save` archive**.
- **Deploy from any image** — DocMan reads the image, works out which volumes it
  needs, which environment variables it is missing, and which ports are free,
  then lets you save the container stopped or start it immediately.
- **Volumes** — create, inspect, see exactly which containers mount them where,
  remove, prune.
- **Host statistics** — real-time host CPU (total and per core), memory, and disk
  I/O, broken down by container.
- **Access** — any number of user accounts, each with its own password,
  WebAuthn passkeys and authenticator app; system-wide bearer tokens for
  the API, an audit trail of every change.

---

# Installing on a Linux docker host

DocMan needs exactly three things: the docker socket, a persistent volume at
`/data`, and a restart policy so it comes back after a reboot.

| What | Why |
| --- | --- |
| `-v /var/run/docker.sock:/var/run/docker.sock` | DocMan manages the host through the engine socket. Without it, nothing works. |
| `-v docman-data:/data` | The SQLite database (account, passkeys, API tokens, fixed addresses, audit) and the TLS key pair. **Without it you lose your login on every restart.** |
| `--restart unless-stopped` | Brings DocMan back after a crash and after a host reboot. |
| `-p 9444:9444` | The HTTPS UI and API. |

## Option A — docker compose (recommended)

Put this in `/opt/docman/docker-compose.yml` on the host:

```yaml
services:
  docman:
    image: docman:1.9.2
    container_name: docman
    restart: unless-stopped
    ports:
      - "9444:9444"
    volumes:
      - docman-data:/data
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      # The hostname you will browse to. Passkeys are bound to a name, so pick
      # one now and always use it.
      DOCMAN_HOSTNAMES: "docman.your-domain.lan"
    security_opt:
      - no-new-privileges:true

volumes:
  docman-data:
```

Then:

```bash
sudo systemctl enable --now docker     # so containers come back after a reboot
cd /opt/docman
docker compose up -d
docker compose logs docman
```

`restart: unless-stopped` only survives a reboot if the docker service itself
starts at boot — that is what `systemctl enable docker` is for, and it is the
step people most often miss.

## Option B — a single docker run

```bash
docker volume create docman-data

docker run -d \
  --name docman \
  --restart unless-stopped \
  -p 9444:9444 \
  -v docman-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --security-opt no-new-privileges:true \
  -e DOCMAN_HOSTNAMES=docman.your-domain.lan \
  docman:1.9.2

docker logs docman
```

## Getting the image onto a host with no registry access

Prebuilt archives are in `dist/`. Pick the one matching the remote host, copy it
over, and load it:

| File | For |
| --- | --- |
| `docman-1.9.2-amd64.tar.gz` | x86-64 — nearly every server and NAS |
| `docman-1.9.2-arm64.tar.gz` | 64-bit ARM — Raspberry Pi 4/5 on a 64-bit OS, Apple silicon, Graviton |
| `docman-1.9.2-armv7.tar.gz` | 32-bit ARM — older Pi and similar |
| `docman-1.9.2-386.tar.gz` | 32-bit x86 |
| `docman-1.9.2-multiarch.tar.gz` | All four in one archive. Needs Docker 25+ with the containerd image store on the receiving host; the single-architecture files load on any version. |

```bash
scp dist/docman-1.9.2-amd64.tar.gz you@dockerhost:/tmp/

# on the docker host
gunzip -c /tmp/docman-1.9.2-amd64.tar.gz | docker load
docker images docman
```

Both tags land: `docman:1.9.2` and `docman:latest`. Check the architecture of the
host first if you are unsure:

```bash
docker info --format '{{.Architecture}}'     # x86_64, aarch64, armv7l …
```

To rebuild the archives yourself:

```bash
docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/386 --build-arg VERSION=1.9.2 -t docman:1.9.2 -t docman:latest --load .
docker save --platform linux/amd64 docman:1.9.2 | gzip -9 > dist/docman-1.9.2-amd64.tar.gz
```

If you have a registry, the usual `docker tag` / `docker push` / `docker pull`
works too — DocMan is an ordinary OCI image.

You can also upload the archive **through DocMan itself** once one instance is
running: **Images → Upload image** streams a `docker save` tar straight into
the engine without creating a container. To update a container whose image is
in no registry, upload the new version under the same tag, then **Recreate**
the container; DocMan remembers the tag was uploaded and leaves "pull first"
unticked, so the container is rebuilt from the version you just uploaded.

## First sign-in

DocMan prints a one-time setup key on its first start:

```
$ docker logs docman
==============================================================
  DocMan 1.0 first-run setup
==============================================================
  Open   https://<this-host>:9444
  Key    QK7MP-2XRTD-9WBHF-LNZ4S
```

1. Browse to `https://<host>:9444`. The certificate is self-signed on first
   run, so accept the warning once.
2. Enter the key, then choose an administrator username and password.
3. Go to **Settings → Passkeys** and register two passkeys. As the second one is
   registered, plain password sign-in switches itself off automatically.
4. Optionally go to **Settings → Account → Authenticator app** and enrol a TOTP
   app. That gives you a second way in that keeps working on any device:
   password plus a six digit code.

The key is regenerated on every restart until setup finishes, and works once.
Lost it? `docker restart docman && docker logs docman`.

Need a fixed key for automated provisioning: set `DOCMAN_SETUP_KEY`.

## Two ways to sign in

DocMan supports both, and you can use either at any time:

| Method | Available when |
| --- | --- |
| **Passkey** | Always, once at least one is registered |
| **Password** | While fewer than two passkeys are registered |
| **Password + authenticator code** | Whenever a TOTP app is enrolled — including after two passkeys, which is what makes it the alternative to passkeys |

Enrolling an authenticator app (**Settings → Account**) turns password sign-in
into two-step and keeps it available permanently, so you are never dependent on
having a passkey-capable device to hand. Scan the QR code with Google
Authenticator, 1Password, Bitwarden, Aegis, Authy or anything else that speaks
TOTP; the secret is also shown as text and as an `otpauth://` link. Each code
works once.

Removing the authenticator later is a single confirmed action, and if two
passkeys are registered at that point password sign-in switches off again.

## Passkeys need a hostname

Browsers refuse to create passkeys for a bare IP address, and only allow them at
all in a secure context. So:

- Reach DocMan by a **name**, not an address: `https://docman.lan:9444`, not
  `https://192.168.1.20:9444`. Add the name to DNS or to the hosts file of the
  machines you administer from, and list it in `DOCMAN_HOSTNAMES` so it lands in
  the self-signed certificate.
- Behind a reverse proxy holding a real certificate, set
  `DOCMAN_TRUST_PROXY=true` and `DOCMAN_RP_ID` to the public hostname.
- If you only want to try it out, password sign-in works fine over plain HTTP on
  `localhost` with `DOCMAN_DISABLE_TLS=true`.

## Firewall

```bash
sudo ufw allow 9444/tcp                  # Debian and Ubuntu
sudo firewall-cmd --add-port=9444/tcp --permanent && sudo firewall-cmd --reload
```

DocMan holds the docker socket, so it is root-equivalent on the host. Open the
port to your management network only.

## SELinux hosts (RHEL, Rocky, Fedora)

The socket mount is usually denied by default. Either relabel it or exempt the
container:

```bash
sudo setsebool -P container_manage_cgroup on
# and add to the run command:
--security-opt label=disable
```

## Upgrading

State lives in the volume, so upgrading is a replace:

```bash
gunzip -c docman-1.1.tar.gz | docker load        # or docker pull
cd /opt/docman && docker compose up -d           # compose
# or:
docker rm -f docman && docker run -d ...same flags... docman:1.9.2
```

The database migrates itself on start. Fixed addresses are re-applied
automatically; watch the log for `addresses:` lines.

## Backing up

Everything that matters is one SQLite file plus the TLS key pair:

```bash
docker run --rm -v docman-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/docman-data.tar.gz -C /data .
```

Restore into a fresh volume the same way with `tar xzf`. Keep it safe: it
contains the password hash, passkey public keys and API token hashes.

---

## Configuration

Everything is an environment variable; there are no flags. `docker run --rm
docman:1.9.2 --help` prints this list.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DOCMAN_DATA_DIR` | `/data` | SQLite database and TLS key pair |
| `DOCMAN_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker endpoint |
| `DOCMAN_HTTPS_ADDR` | `:9444` | HTTPS listener |
| `DOCMAN_HTTP_ADDR` | `:9080` | HTTP listener; redirects to HTTPS |
| `DOCMAN_TLS_CERT` / `DOCMAN_TLS_KEY` | — | Use your own certificate |
| `DOCMAN_DISABLE_TLS` | `false` | Serve plain HTTP only |
| `DOCMAN_HOSTNAMES` | — | Extra names for the self-signed certificate |
| `DOCMAN_RP_ID` | request host | Hostname passkeys are bound to |
| `DOCMAN_SESSION_TTL` | `12h` | Browser session lifetime |
| `DOCMAN_TRUST_PROXY` | `false` | Honour `X-Forwarded-*` |
| `DOCMAN_PROC_PATH` | `/proc` | Where host metrics are read from |
| `DOCMAN_SETUP_KEY` | generated | Fixed first-run key, for automation |

Publishing port 9080 as well gives you an HTTP listener that redirects to HTTPS,
which is convenient but optional.

## Help and the API

A running instance serves its own documentation, with no sign-in needed:

- `/help` — the user guide: every screen and task, from first sign-in to
  updating containers, with search (press `/`). The source is the Markdown in
  [`help/`](help/), embedded in the binary; the page order lives in
  `internal/srv/help.go`.
- `/help/api` — the API reference, one page per area, from [API.md](API.md).
- `GET /api` — the same reference as raw Markdown for anything that is not a
  browser, so an agent can read it directly:

```bash
curl -sk https://docman.lan:9444/api > api.md
```

Any help page is available as Markdown with `?format=raw`.

## Building

Multi-architecture, for x86-64, 32-bit x86, arm64 and armv7:

```bash
docker buildx build \
  --platform linux/amd64,linux/386,linux/arm64,linux/arm/v7 \
  --build-arg VERSION=1.9.2 \
  -t docman:1.9.2 .
```

A single architecture you can run immediately:

```bash
docker build --build-arg VERSION=1.9.2 -t docman:1.9.2 .
```

Locally, without docker:

```bash
go mod tidy
go build -o docman .
DOCMAN_DATA_DIR=./data DOCMAN_DISABLE_TLS=true ./docman
```

The build needs the network once, to fetch `modernc.org/sqlite` — the only
third-party dependency. The Docker API client, the WebAuthn verifier, the
WebSocket server, the terminal emulator and the charts are all part of DocMan.

**If your network intercepts TLS** (Netskope, Zscaler and friends), the build
container will not trust the proxy's certificate and `go mod tidy` will fail.
Export your root CA to `build/certs/` as a PEM file and the build trusts it:

```powershell
# Windows: export the machine's trusted roots
$dir = "build\certs"; New-Item -ItemType Directory -Force $dir | Out-Null
Get-ChildItem Cert:\LocalMachine\Root | ForEach-Object {
  "-----BEGIN CERTIFICATE-----"
  [Convert]::ToBase64String($_.RawData, 'InsertLineBreaks')
  "-----END CERTIFICATE-----"
} | Out-File "$dir\host-roots.crt" -Encoding ascii
```

```bash
# Linux or macOS
cp /usr/local/share/ca-certificates/corporate-root.crt build/certs/
```

Those files are gitignored and used only during the build; the runtime image
ships the stock CA bundle.

## How configuration changes work

Docker cannot change a container's environment, command, ports or mounts in
place. When you apply a configuration change DocMan:

1. reads the container's full inspect document, so settings it does not model
   are carried over untouched;
2. applies your edits on top;
3. stops the container and renames it aside;
4. creates the replacement under the original name, reattaching every network
   and reapplying any fixed address;
5. deletes the original — and if step 4 failed, renames the original back and
   restarts it instead.

Named volumes and fixed addresses survive. Anything written to the container's
own filesystem outside a volume does not, which is true of any recreate.

## Security notes

- Passwords are stored as PBKDF2-HMAC-SHA256 with 600,000 iterations.
- Sessions are opaque random tokens in a `HttpOnly`, `SameSite=Strict` cookie;
  cookie-authenticated writes also require a header a cross-site request cannot
  set. WebSocket upgrades check `Origin`.
- API tokens are shown once and stored only as a SHA-256 hash.
- Passkey registration accepts `none` attestation and verifies origin, relying
  party and signature. Signature counters are checked when the authenticator
  keeps one.
- TOTP is RFC 6238 with HMAC-SHA1, six digits and a thirty second step, with one
  step of skew. Codes are single use: the step a code came from is recorded and
  earlier steps are refused. The enrolment secret is only persisted once a
  working code proves it, and a wrong password and a wrong code are reported
  identically so sign-in cannot be used as a password oracle.
- Sign-in attempts and setup-key attempts are rate limited per address.
- `GET /api` and `GET /api/state` are readable without signing in; everything
  else requires a session or a bearer token.
- **DocMan has the docker socket, so DocMan is root on the host.** Treat access
  to it exactly as you would treat shell access. Do not expose it to the
  internet without a reverse proxy you trust.

## License

DocMan is open source under the [Apache License 2.0](LICENSE): use it, change
it, fork it and redistribute it, commercially or not. Anything you distribute
that is based on DocMan must keep the [NOTICE](NOTICE) file's attribution to
the original project, <https://github.com/mwareman/docman>.
