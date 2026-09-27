# Install, upgrade, back up

This page is for whoever looks after the host DocMan runs on: installing it, configuring it, upgrading it and backing it up.

## What DocMan needs

DocMan runs as a container on the Docker host it manages, and needs three things:

| What | Why |
| --- | --- |
| The Docker socket, `-v /var/run/docker.sock:/var/run/docker.sock` | DocMan manages the host through it. Without it nothing works. |
| A volume at `/data`, such as `-v docman-data:/data` | Holds DocMan's database (account, passkeys, API tokens, fixed addresses, preferences, activity log) and its TLS key pair. Without it you lose your sign-in on every restart. |
| A restart policy, `--restart unless-stopped` | Brings DocMan back after a crash or a reboot. |

Port `9444` serves the UI and the API over HTTPS. Port `9080`, if you publish it, redirects plain HTTP to HTTPS.

## Installing with Docker Compose

Put this in `/opt/docman/docker-compose.yml`:

```yaml
services:
  docman:
    image: docman:latest
    container_name: docman
    restart: unless-stopped
    ports:
      - "9444:9444"
    volumes:
      - docman-data:/data
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      DOCMAN_HOSTNAMES: "docman.example.lan"
    security_opt:
      - no-new-privileges:true

volumes:
  docman-data:
```

Then start it and read the setup key from its log:

```bash
sudo systemctl enable --now docker
cd /opt/docman
docker compose up -d
docker compose logs docman
```

`systemctl enable docker` makes Docker, and so every container with a restart policy, start at boot. It is the step most often missed.

## Installing with docker run

```bash
docker volume create docman-data
docker run -d --name docman --restart unless-stopped \
  -p 9444:9444 \
  -v docman-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --security-opt no-new-privileges:true \
  -e DOCMAN_HOSTNAMES=docman.example.lan \
  docman:latest
docker logs docman
```

Then continue with [Setting up DocMan](/help/getting-started).

## Hosts without registry access

DocMan's image can be delivered as an archive. Pick the one for the host's architecture (`docker info --format '{{.Architecture}}'` tells you), copy it over and load it:

```bash
gunzip -c docman-amd64.tar.gz | docker load
```

## Configuration

DocMan takes no command-line flags; everything is an environment variable. `docker run --rm docman --help` prints this list.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DOCMAN_DATA_DIR` | `/data` | Where the database and TLS key pair live. |
| `DOCMAN_DOCKER_HOST` | `unix:///var/run/docker.sock` | The Docker endpoint to manage. |
| `DOCMAN_HTTPS_ADDR` | `:9444` | The HTTPS listen address. |
| `DOCMAN_HTTP_ADDR` | `:9080` | The HTTP listen address, which redirects to HTTPS. |
| `DOCMAN_TLS_CERT` and `DOCMAN_TLS_KEY` | none | Paths to your own certificate and private key. |
| `DOCMAN_DISABLE_TLS` | `false` | Serve plain HTTP only. Passkeys then work only on `localhost` or behind an HTTPS proxy. |
| `DOCMAN_HOSTNAMES` | none | Comma-separated names to put in the self-signed certificate. |
| `DOCMAN_RP_ID` | the browser's host | The host name passkeys are bound to. |
| `DOCMAN_SESSION_TTL` | `12h` | How long a browser session lasts, such as `8h`. A bare number is taken as hours. |
| `DOCMAN_TRUST_PROXY` | `false` | Trust `X-Forwarded-*` headers from a reverse proxy. |
| `DOCMAN_PROC_PATH` | `/proc` | Where host metrics are read from. |
| `DOCMAN_SETUP_KEY` | generated | A fixed first-run setup key, for automated installs. |

### Using your own certificate

On first start DocMan creates a self-signed certificate in `/data`, covering the names in `DOCMAN_HOSTNAMES`. To use a certificate from your own certificate authority instead, mount the files into the container and point `DOCMAN_TLS_CERT` and `DOCMAN_TLS_KEY` at them.

### Behind a reverse proxy

When a proxy such as nginx, Traefik or Caddy terminates HTTPS in front of DocMan:

- Set `DOCMAN_TRUST_PROXY=true` so DocMan sees the real client address and scheme.
- Set `DOCMAN_RP_ID` to the public host name, so passkeys are bound to it.
- The proxy must pass WebSocket upgrades through: live logs, statistics and the console all use them.
- Request size limits need no changes. Image and file uploads are sent in pieces that DocMan sizes automatically to whatever the proxy allows, and recreates run on the server, so they finish even if the proxy itself is the container being upgraded.

## Upgrading

DocMan's state lives in its `/data` volume, so upgrading means replacing the container with one running the new image. The database migrates itself on start, and fixed addresses are reapplied automatically; the log shows `addresses:` lines as it does so.

**From within DocMan (version 1.1 and later):** load the new image, by pulling it or with **Images → Upload image**, then **Recreate** DocMan's own container. See [Updating DocMan itself](/help/updating#updating-docman-itself).

**From the host:** load the new image, then recreate the container:

```bash
gunzip -c docman-amd64.tar.gz | docker load
cd /opt/docman && docker compose up -d --force-recreate docman
```

Without Compose, `docker rm -f docman` and run the same `docker run` command as before.

> **Warning:** Upgrade DocMan 1.0 from the host. Recreating version 1.0 from inside itself leaves the new container not running.

## Backing up

Everything DocMan needs is in its `/data` volume: one SQLite database and the TLS key pair. Back it up with a throwaway container:

```bash
docker run --rm -v docman-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/docman-data.tar.gz -C /data .
```

Restore into a fresh volume the same way with `tar xzf`, before starting DocMan. Keep the backup safe: it contains the password hash, passkey public keys and fingerprints of API tokens.

This backs up DocMan, not your containers' data. Back up the volumes your applications use as well; see [Backing up a volume](/help/volumes#backing-up-a-volume).

## Security

- **DocMan is root on the host.** It holds the Docker socket, and anyone who can sign in can do anything on the host. Open port 9444 to your management network only, and never expose it to the internet without a reverse proxy you trust.
- **Every user is a full administrator**, and so is every API token. Add only people you would trust with root on the host, remove accounts when people leave, and revoke tokens that are no longer used; **Last signed in** and **Last used** in Settings show which are idle.
- Use passkeys, or a password with an authenticator app, rather than a password alone. See [Signing in](/help/sign-in).
- Passwords are stored as salted PBKDF2-SHA256 hashes; API tokens only as SHA-256 fingerprints.
- Browser sessions use a secure, HTTP-only cookie, and every change also needs a header that other websites cannot send.
- Sign-in and setup-key attempts are rate limited per address.
- `/help`, `/api` and `/api/state` are readable without signing in. They describe DocMan, not your host. Everything else needs a session or an API token.

### Firewall

```bash
sudo ufw allow 9444/tcp
sudo firewall-cmd --add-port=9444/tcp --permanent && sudo firewall-cmd --reload
```

Use the first on Debian and Ubuntu, the second on RHEL, Rocky and Fedora.

### SELinux hosts

On RHEL, Rocky and Fedora, SELinux usually blocks access to the Docker socket from a container. Add `--security-opt label=disable` to the `docker run` command, or `label=disable` under `security_opt` in Compose.
