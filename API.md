# DocMan API

Everything the UI does is available here. Base URL is the DocMan origin, e.g.
`https://docman.local:9444`. All bodies and responses are JSON unless noted.

## Auth

```
Authorization: Bearer dm_<prefix>.<secret>
```

Create a token in **Settings → API tokens**, or `POST /api/tokens` from an
existing session. Tokens carry full administrator rights. The secret is shown
once; DocMan stores only its SHA-256.

- Bearer requests need no other headers.
- Cookie (browser) requests must add `X-DocMan-CSRF: 1` on every non-GET.
- WebSockets accept `?access_token=<token>` on `/api/stream/*` paths.
- Self-signed certificate by default: `curl -k`, or install the cert from
  `/data/tls-cert.pem`.

### How sign-in is allowed

Each user account follows these rules on its own. Two independent ways in, and
both stay available:

- **A passkey** — always accepted.
- **Username and password**, accepted while *either* fewer than two passkeys are
  registered, *or* an authenticator app is enrolled. Once one is enrolled the
  password is always two-step: password plus a six digit code in the same
  request.

So registering two passkeys switches plain password sign-in off, and enrolling
an authenticator app switches it back on as password + one-time code.
Before sign-in, `GET /api/state` reports whether *any* account can use a
password (`password_enabled`), whether any uses an authenticator app
(`totp_enabled`) and whether any passkey exists (`passkey_ready`), so a client
knows which fields to offer without learning anything about a particular
account. Once signed in, the same fields describe the caller's own account.

Errors are `{"error":"…","hint":"…"}` with a matching HTTP status. Docker's own
status codes are passed through (404 unknown container, 409 conflict, 502 when
the engine is unreachable).

```bash
export D=https://docman.local:9444 T=dm_ab12cd34.xxxxx
curl -sk -H "Authorization: Bearer $T" $D/api/containers | jq '.containers[].name'
```

## Reading this reference

DocMan serves this reference from the running container, so it always matches
the build you are talking to. None of it needs authentication.

- In a browser it is part of the help system at [/help/api](/help/api), one
  page per area, or [all on one page](/help/api/all) for searching and printing.
- `GET /api` sends a browser there. **Anything else** — curl, a script, an
  agent — gets the raw Markdown, which is the more useful form for a machine.
- Force either with `?format=raw` or `?format=html`; `/api.md` is always raw.
- The user guide is at [/help](/help), and any help page is available as
  Markdown with `?format=raw`.

```bash
curl -sk $D/api > api.md          # raw Markdown, ready to feed to a tool
```

## Quick reference

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api` | public: this reference as raw Markdown; browsers go to `/help/api` |
| GET | `/help`, `/help/{page}` | public: the user guide and this reference, formatted |
| GET | `/help/search.json` | public: the search index the help pages use |
| GET | `/api/state` | public: setup and sign-in state |
| GET | `/api/system` | engine info, DocMan version, managed network |
| GET | `/api/system/df` | image/volume disk usage |
| GET | `/api/system/metrics` | one host CPU/memory/disk sample |
| GET | `/api/audit?limit=200` | recent changes |
| GET | `/api/settings` · PATCH | preferences |
| GET | `/api/registries` · POST | list (with the kinds offered) / add a registry |
| PUT | `/api/registries/{id}` · DELETE | change / remove a registry |
| POST | `/api/registries/{id}/default` | make it the default |
| POST | `/api/registries/{id}/test` | check the address and sign-in |
| GET | `/api/registries/{id}/search?q=` | search the registry |
| GET | `/api/registries/{id}/tags?repo=` | list an image's tags |
| GET | `/api/registries/{id}/archives` | a GitHub repository's pre-built images |
| POST | `/api/registries/{id}/install` | install one (ndjson progress) |
| GET | `/api/containers?all=1` | list containers |
| POST | `/api/containers` | create (and optionally start) |
| GET | `/api/containers/{id}` | one container, list shape |
| GET | `/api/containers/{id}/inspect` | raw docker inspect |
| GET | `/api/containers/{id}/spec` | editable config |
| PUT | `/api/containers/{id}/spec` | apply config (recreates) |
| POST | `/api/containers/{id}/start` | start |
| POST | `/api/containers/{id}/stop?t=10` | stop |
| POST | `/api/containers/{id}/restart?t=10` | restart |
| POST | `/api/containers/{id}/pause` · `/unpause` | freeze / resume |
| POST | `/api/containers/{id}/kill?signal=SIGKILL` | signal |
| POST | `/api/containers/{id}/recreate` | rebuild, optionally pulling first (a job) |
| GET | `/api/jobs` · `/api/jobs/{id}` | background recreates and their progress |
| GET | `/api/volumes/{name}/files?path=` | browse a volume (see Files in a volume) |
| POST | `/api/containers/{id}/rename` | rename |
| DELETE | `/api/containers/{id}?force=1&volumes=0` | remove |
| GET | `/api/containers/{id}/logs?tail=500` | logs as one string |
| GET | `/api/containers/{id}/stats` | one stats sample |
| GET | `/api/containers/{id}/top` | processes |
| POST | `/api/containers/{id}/ip` | fix the internal address |
| DELETE | `/api/containers/{id}/ip` | release it |
| GET | `/api/images` | list images |
| GET | `/api/images/{ref}` | inspect + defaults + history |
| POST | `/api/images/pull` | pull (ndjson progress) |
| POST | `/api/images/upload` | import a tar in one request (ndjson progress) |
| POST | `/api/uploads` · `/api/uploads/{id}` · `…/complete` | upload in pieces, through any proxy limit |
| POST | `/api/images/tag` | add a tag |
| POST | `/api/images/remove` | remove or untag |
| POST | `/api/images/prune?unused=0` | prune |
| POST | `/api/images/deploy-plan` | what an image needs to run |
| GET | `/api/images/updates` | latest registry update checks |
| POST | `/api/images/updates/check` | check registries for newer versions now |
| GET | `/api/system/volume-drivers` | the host's volume drivers |
| GET | `/api/volumes` · POST | list / create |
| GET | `/api/volumes/{name}` · DELETE | inspect / remove |
| POST | `/api/volumes/prune` | prune unused |
| GET | `/api/networks` · `/api/networks/{id}` | docker networks |
| GET | `/api/addresses` | managed network + every fixed address |
| POST | `/api/addresses/reconcile` | re-apply addresses that drifted |
| GET | `/api/users` · POST | list / add user accounts |
| POST | `/api/users/{id}/reset` | new password; remove that user's passkeys, authenticator and sessions |
| DELETE | `/api/users/{id}` | remove a user account |
| GET | `/api/account`, `/api/passkeys` | the signed-in user's own sign-in (session only) |
| GET | `/api/tokens` · POST | list / create API tokens |
| POST | `/api/account/totp/begin` | start authenticator enrolment |
| POST | `/api/account/totp/enable` | confirm it with a code |
| POST | `/api/account/totp/disable` | remove it |

`{id}` accepts a container id, a short id prefix, or a name.

## Containers

### List

`GET /api/containers?all=1` (default includes stopped)

```json
{"containers":[{
  "id":"3f9c…","name":"web","image":"nginx:alpine","image_id":"sha256:…",
  "command":"nginx -g 'daemon off;'","created":1736200000,
  "state":"running","status":"Up 3 hours (healthy)","health":"healthy",
  "ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"}],
  "labels":{},"networks":{"docman0":"172.20.0.11"},
  "primary_ip":"172.20.0.11","pinned_ip":"172.20.0.11","pinned":true,
  "mounts":[{"Type":"volume","Name":"web-data","Destination":"/data","RW":true}],
  "self":false,
  "update_available":true,"update_image":"nginx:alpine","update_source":"registry",
  "image_origin":"registry"
}]}
```

`state` is one of `created running paused restarting exited dead removing`.
`self:true` marks DocMan's own container.

`update_available:true` means a newer version of the container's image
(`update_image`) exists while the container still runs its original one.
`update_source` says where:

- `host` — the newer image is already here under the container's tag, typically
  after an upload, a `docker load` or a pull. `POST …/recreate` with
  `"pull":false` switches over.
- `registry` — the [daily registry check](/help/api/images) found a newer
  version that has not been pulled. `POST …/recreate` with `"pull":true` fetches
  it and switches over.

It is left out when the tagged image is older, and for containers created from
an image ID or digest. `image_origin` is where the image came from —
`registry`, `uploaded`, `local` (built on the host, or in no registry DocMan can
reach) or `unknown` (not checked yet) — and is how the UI decides whether a
recreate pulls first.
`image` is always the reference the container was created with, even after the
tag has moved to another image.

### The container spec

`GET /api/containers/{id}/spec` →
`{"spec":{…},"running":true,"pinned_ip":"172.20.0.11","networks":{…}}`

The spec is the editable view. Send the same shape back to `PUT …/spec` or to
`POST /api/containers`.

```json
{
  "name":"web","image":"nginx:alpine",
  "entrypoint":[],"cmd":["nginx","-g","daemon off;"],
  "env":["TZ=UTC","APP_MODE=prod"],
  "labels":{"team":"platform"},
  "working_dir":"","user":"","hostname":"","tty":false,"open_stdin":false,
  "stop_signal":"SIGQUIT",
  "ports":[{"container_port":80,"protocol":"tcp","host_ip":"","host_port":"8080"}],
  "mounts":[
    {"type":"volume","source":"web-data","target":"/data","read_only":false},
    {"type":"bind","source":"/srv/site","target":"/usr/share/nginx/html","read_only":true},
    {"type":"tmpfs","source":"","target":"/run","read_only":false}
  ],
  "restart_policy":"unless-stopped","restart_retries":0,"auto_remove":false,
  "network_mode":"bridge","dns":[],"extra_hosts":[],
  "privileged":false,"read_only_rootfs":false,
  "cap_add":[],"cap_drop":[],"devices":[],"group_add":[],"security_opt":[],
  "memory":0,"memory_swap":0,"nano_cpus":0,"cpu_shares":0,"pids_limit":0,
  "shm_size":0,"sysctls":{},
  "log_driver":"json-file","log_options":{},"runtime":""
}
```

Rules:

- `entrypoint` / `cmd`: empty array means "use the image default".
- `mounts[].type`: `volume` (named, or anonymous when `source` is empty),
  `bind` (absolute host path, created if missing), or `tmpfs`.
- `ports[].host_port`: empty string means docker picks a free port.
- `memory` and `shm_size` are bytes; `nano_cpus` is CPU-seconds × 1e9
  (`1500000000` = 1.5 cores). `0` means unlimited.
- `restart_policy`: `no` `always` `unless-stopped` `on-failure`.
- `network_mode` must exist on this host: `bridge`, `host`, `none`, the name or
  ID of a Docker network, or `container:<name or id>` of an existing container.
  Anything else is refused with `400`. A container's current value is always
  accepted unchanged, even if that network has since been removed.
- `cap_add` / `cap_drop`: Linux capability names, with or without `CAP_`, in any
  case, or `ALL`. The container gets Docker's 14 default capabilities, minus
  `cap_drop`, plus `cap_add`. Docker reports them back in the form
  `CAP_NET_ADMIN`.
- Fields the spec does not cover (health checks, ulimits, …) are preserved
  across a spec update because DocMan starts from the live inspect document.

### Create

`POST /api/containers`

```json
{"spec":{…},"start":true,"pin_ip":true,"ip":""}
```

`pin_ip` fixes the internal address after creating; `ip` empty means DocMan
allocates the next free one. Named volumes referenced by `mounts` are created
if they do not exist.

→ `201 {"ok":true,"id":"…","name":"web","started":true,"pinned_ip":"172.20.0.11","network":"docman0"}`

A container that was created but would not start returns `202` with
`start_error`. A failed pin returns `pin_error` alongside a successful create.

### Apply a configuration change

`PUT /api/containers/{id}/spec`

```json
{"spec":{…},"start":true}
```

Docker cannot change env, command, ports or mounts in place, so DocMan stops the
container, sets it aside, creates the replacement under the same name with the
same networks and fixed address, then deletes the original. If the replacement
cannot be created, the original is renamed back and restarted. Omit `start` to
keep whatever state it was in.

The spec replaces the container's whole configuration: anything it leaves out is
cleared. Always start from `GET …/spec`, change what you need and send the whole
spec back. A spec without an `image` is refused with `400`, which catches the
common mistake of sending only the fields being changed.

→ `202 {"ok":true,"job":{…}}` — the change runs as a background job; see
[Jobs](#jobs) below.

### Recreate

`POST /api/containers/{id}/recreate`

```json
{"pull":true,"start":true,"image":"nginx:1.27-alpine"}
```

All fields optional. `pull:true` fetches the newest image first — the usual way
to update a container in place.

For an image that is in no registry, upload the new version with
`POST /api/images/upload` under the same tag, then recreate with `pull:false`:
the container is rebuilt from whatever the tag now points at. DocMan remembers
which tags were uploaded (a successful pull of the tag clears it) and reports
`"image_uploaded":true` on `GET /api/containers/{id}` for them; the UI uses it
to leave "pull first" unticked.

→ `202 {"ok":true,"job":{…}}` — see [Jobs](#jobs).

Recreating DocMan's own container (here or through `PUT …/spec`) ends in the
job state `self`: a short-lived helper container, labelled
`docman.helper=replace-self`, does the swap a few seconds later, so expect the
API to drop out briefly and the job to be unknown to the new instance. The
helper removes itself when done; if it fails, the original DocMan is restored
and removes the helper at startup, copying its log into DocMan's own.

### Jobs

Recreates and configuration changes never run inside the request. Stopping a
container can cut off the connection that asked for it — when it is the proxy
or tunnel in front of DocMan — so the request only starts a job and returns at
once, and the job runs to the end on DocMan whatever happens to the client.

```
GET /api/jobs            # recent jobs, newest first
GET /api/jobs/{id}       # one job
```

```json
{"id":"9f2c61d04b7e1a35","kind":"recreate","container":"proxy","old_id":"3f9c…",
 "new_id":"8ab1…","state":"done","step":"Finished","actor":"jsmith",
 "started_at":1736200000,"ended_at":1736200006}
```

- `state` is `running` (with `step` saying what it is doing: pulling,
  stopping, creating the replacement, starting), `done`, `failed` (with
  `error`, and `new_id` when the replacement exists but would not start), or
  `self` when DocMan handed its own recreate to a helper.
- The container comes back in the state it was in — running, stopped or paused
  — unless `start` was given.
- Poll until `state` is no longer `running`. If a poll fails because the
  connection dropped, keep polling; the job is not affected. A `404` means
  DocMan restarted since; look the container up by name.
- `GET /api/containers/{old id}` for a container that was recreated answers
  `404 {"error":"this container was recreated","moved_to":"<new id>"}`.

```bash
job=$(curl -sk -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"pull":true}' $D/api/containers/web/recreate | jq -r .job.id)
while [ "$(curl -sk -H "Authorization: Bearer $T" $D/api/jobs/$job | jq -r .state)" = running ]; do sleep 2; done
curl -sk -H "Authorization: Bearer $T" $D/api/jobs/$job | jq '{state, new_id, error}'
```

### Logs

One-shot: `GET /api/containers/{id}/logs?tail=500&timestamps=1` →
`{"logs":"…"}` (stdout and stderr, already de-multiplexed).

Live: WebSocket, below.

### Statistics

`GET /api/containers/{id}/stats` returns one derived sample. CPU percentage
needs two readings, so a single call may report `cpu_percent: 0`; use the
WebSocket for anything real.

```json
{"id":"3f9c…","name":"web","t":1736200000123,
 "cpu_percent":4.2,"cpu_cores":0.17,"online_cpus":4,
 "mem_usage":52428800,"mem_limit":2147483648,"mem_percent":2.4,
 "blk_read":10485760,"blk_write":2097152,"blk_read_ps":0,"blk_write_ps":1024,
 "net_rx":9000,"net_tx":4000,"net_rx_ps":120,"net_tx_ps":80,"pids":7}
```

## Fixed internal addresses

DocMan keeps one bridge network of its own (`docman0` by default) with a private
subnet chosen to avoid every existing network on the host. The lower half of
that subnet is DocMan's to allocate; the upper half is left to docker.

`POST /api/containers/{id}/ip` with `{"ip":""}` or `{"ip":"172.20.0.25"}`:

→ `{"container":"web","network":"docman0","ip":"172.20.0.25","applied":true}`

The container is attached to the managed network at that address and detached
from the default bridge, so the address is the only one it has and it does not
change. It survives restarts and recreates. Works on running and stopped
containers. Fails for `network_mode` of `host`, `none` or `container:…`.

`DELETE /api/containers/{id}/ip` releases it and returns the container to the
default bridge.

`GET /api/addresses`:

```json
{"provisioned":true,
 "network":{"name":"docman0","subnet":"172.20.0.0/16","gateway":"172.20.0.1",
            "static_first":"172.20.0.10","static_last":"172.20.127.255",
            "dynamic_from":"172.20.128.0/17"},
 "addresses":[{"container":"web","network":"docman0","ip":"172.20.0.11",
               "live_ip":"172.20.0.11","exists":true,"in_sync":true,
               "created_at":1736200000}]}
```

`in_sync:false` means the container's live address no longer matches the
reservation — call `POST /api/addresses/reconcile` (or re-pin) to fix it.

`reconcile`, which also runs at every start, first rebuilds reservations missing
from DocMan's database: any container on DocMan's labelled `docman0` network
with an explicitly requested (static) address is recorded again. So a lost or
replaced data volume does not lose the fixed addresses; the response's `notes`
list each one recovered.

## Images

`GET /api/images` → each entry is a docker image summary plus
`reference` (the best display name) and `in_use` (container count).

`GET /api/images/{ref}` → `{"inspect":{…},"defaults":{…},"history":[…]}`.
`{ref}` may contain slashes and a tag: `/api/images/ghcr.io/acme/api:1.4`.

### Pull

`POST /api/images/pull` `{"image":"nginx","tag":"alpine","registry_id":0,"username":"","password":""}`

Credentials, in order: `username`/`password` when given (used once, not stored);
else the saved sign-in of registry `registry_id`; else the saved sign-in of the
registry whose address the image name starts with (see [Registries](#registries)).

Response is `application/x-ndjson`: docker's own progress objects, one per line,
flushed as they arrive.

### Upload an archive

`POST /api/images/upload` with the **raw tar as the request body** (the output of
`docker save`), streamed straight to the engine. Response is ndjson; the
imported reference appears as `{"stream":"Loaded image: myapp:1.0\n"}`.

```bash
docker save myapp:1.0 | \
  curl -sk -X POST --data-binary @- -H "Authorization: Bearer $T" \
    -H "Content-Type: application/x-tar" $D/api/images/upload
```

This is one request, so a proxy or WAF in front of DocMan — Cloudflare,
cloudflared, nginx, lighttpd — refuses or cuts it off once it passes its request
size limit. Through a proxy, upload in pieces instead.

### Upload in pieces

Any size, through any proxy. The file is sent as a series of pieces, each its
own request, into a temporary file in DocMan's data volume; once every byte is
there it is imported as a [job](/help/api/containers#jobs).

```
POST   /api/uploads              {"kind":"image","name":"app.tar","size":123456789}  → {id, received, max_chunk}
POST   /api/uploads/{id}?offset=N   body: the next piece (PUT works too)             → {received, size}
GET    /api/uploads/{id}         # how much DocMan holds, to resume after a failure
POST   /api/uploads/{id}/complete   → 202 {job}
DELETE /api/uploads/{id}         # cancel and discard
```

- `kind` is `image` for a `docker save` archive, or `file` with `volume`,
  `path` (a folder) and optionally `overwrite` to put a file into a volume.
- Each piece needs a `Content-Length` and may be up to `max_chunk` bytes
  (256 MB). A piece that arrives incomplete is discarded; the response carries
  `received`, the offset to send from next. An `offset` out of step answers
  `409` with `received`.
- A proxy's own refusal (`413`, an HTML error page, a dropped connection) means
  the piece was too big for it: send it again, smaller, from the `received`
  offset. The UI starts at 8 MB and halves after each failure.
- `complete` refuses (`409`) until `received` equals `size`. The job's `step`
  reports `Importing into Docker: N%`; when `done`, `result` lists the images
  loaded. Uploads left unfinished for a day are discarded, as are all
  temporary files when DocMan restarts.

```bash
f=app.tar; size=$(stat -c %s $f); piece=$((4*1024*1024))
id=$(curl -sk -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"kind\":\"image\",\"name\":\"$f\",\"size\":$size}" $D/api/uploads | jq -r .id)
for ((off=0; off<size; off+=piece)); do
  tail -c +$((off+1)) $f | head -c $piece | curl -sk -X POST --data-binary @- \
    -H "Authorization: Bearer $T" "$D/api/uploads/$id?offset=$off" > /dev/null
done
job=$(curl -sk -X POST -H "Authorization: Bearer $T" $D/api/uploads/$id/complete | jq -r .job.id)
```

### Deploy plan

`POST /api/images/deploy-plan` `{"image":"postgres:16","name":""}`

Reads the image and works out what it needs, ready to be edited and submitted to
`POST /api/containers`.

```json
{"image":"postgres:16","name":"postgres",
 "defaults":{"entrypoint":["docker-entrypoint.sh"],"cmd":["postgres"],
             "env":["PATH=…","POSTGRES_PASSWORD="],"ports":[{"container_port":5432,"protocol":"tcp"}],
             "volumes":["/var/lib/postgresql/data"],"architecture":"amd64","os":"linux","size":438000000},
 "env":[{"key":"POSTGRES_PASSWORD","value":"","secret":true,"required":true,
         "note":"the image leaves this empty, so it probably needs a value"}],
 "mounts":[{"target":"/var/lib/postgresql/data","type":"volume","source":"postgres-postgresql-data",
            "note":"the image stores data here, so it needs a volume to survive a restart"}],
 "ports":[{"container_port":5432,"protocol":"tcp","host_port":"5432","publish":true}],
 "notes":["this image runs as root; consider setting a user"],
 "spec":{…ready-to-submit container spec…}}
```

`env[].required` marks variables the image left empty. `env[].secret` marks
names that look like credentials. Host ports are checked against what other
containers already publish and moved if taken.

### Registry update checks

Once a day, and on request, DocMan asks the registry of every image tag that
was pulled from one which digest the tag points at now — through the Engine, so
nothing is downloaded — and compares it with the image on this host.

```
GET  /api/images/updates                # last results, and when checks ran and run next
POST /api/images/updates/check          {} checks every image; {"reference":"nginx:alpine"} one
```

```json
{"checked_at":1736200000,"next_check_at":1736286400,
 "checks":[{"reference":"nginx:alpine","status":"update","local_digest":"sha256:…",
            "remote_digest":"sha256:…","checked_at":1736200000}]}
```

`status` is `current`, `update`, or `unavailable` with a `detail`: not from a
registry, not in a registry DocMan can reach (not found, or needs credentials
DocMan does not keep), or the registry could not be reached. Uploaded images
are not checked. A pull re-checks its tag automatically.

`GET /api/images` includes each image's `origin` (`registry`, `uploaded`,
`local`, `unknown`), its latest `update` check, and `in_use_by`: the names of
the containers created from it.

### Other image operations

```
POST /api/images/tag     {"image":"myapp:1.0","repo":"myapp","tag":"stable"}
POST /api/images/remove  {"image":"myapp:1.0","force":false}
POST /api/images/prune?unused=1        # unused=0 removes only dangling images
```

## Volumes

```
GET    /api/volumes                    # each volume plus used_by[]
POST   /api/volumes                    {"name":"web-data","driver":"local","options":{}}
GET    /api/volumes/{name}
DELETE /api/volumes/{name}?force=0
POST   /api/volumes/prune
GET    /api/system/volume-drivers      # {"drivers":["local", …]}
```

`driver` must be one of the host's volume drivers: `local`, plus any enabled
volume plugins. Anything else is refused with `400`, listing the ones available.
An empty `driver` means `local`.

`used_by` tells you exactly who holds a volume:

```json
{"volumes":[{"Name":"web-data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/web-data/_data",
  "used_by":[{"container":"web","target":"/data","read_only":false,"state":"running"}]}]}
```

### Files in a volume

The volume explorer's API. Paths are inside the volume and start at `/`;
nothing outside it can be named.

```
GET    /api/volumes/{name}/files?path=/config            # list a folder
GET    /api/volumes/{name}/files/download?path=/a.yml    # a file, as itself
GET    /api/volumes/{name}/files/download?path=/config&kind=dir   # a folder, as .tar
GET    /api/volumes/{name}/files/content?path=/a.yml     # a text file up to 1 MB
PUT    /api/volumes/{name}/files/content?path=/a.yml     {"content":"…","mtime":1736200000}
POST   /api/volumes/{name}/files/upload?path=/config&name=a.yml   # body: the file
POST   /api/volumes/{name}/files/mkdir                   {"path":"/config/new"}
POST   /api/volumes/{name}/files/rename                  {"from":"/a.yml","to":"/old/a.yml"}
DELETE /api/volumes/{name}/files?path=/old               # a file, or a folder and its contents
DELETE /api/volumes/{name}/explorer                     # done: stop the volume's helper now
```

The first call starts a helper container for the volume. It is removed by
`DELETE …/explorer`, or after three minutes without a call.

```json
{"path":"/config","docman_data":false,"truncated":false,
 "dir":{"name":"config","type":"dir","uid":1000,"gid":1000,…},
 "entries":[{"name":"app.yml","type":"file","size":22,"mode":"-rw-r--r--","perm":420,
             "uid":1000,"gid":1000,"mtime":1736200000}]}
```

- `type` is `dir`, `file`, `link` (with `target`) or `other`. Listings stop at
  5,000 entries with `truncated:true`.
- `content` refuses files over 1 MB (`413`) and binary files (`415`).
- Saving with the `mtime` the file had when it was read refuses (`409`) if it
  has changed since. Send `"create":true` to make a new file, which refuses if
  the name exists. Saves keep the file's owner and permissions.
- Uploads stream, so size is limited only by the volume. They need a
  `Content-Length`, refuse to replace a file (`409` with `exists:true`) unless
  `&overwrite=1`, and — like new folders — take the owner of the folder they
  land in.
- `docman_data:true` marks DocMan's own data volume.
- DocMan does this through a helper container per volume, removed after ten
  minutes without use and before the volume is deleted or pruned. Every change
  is recorded in the audit log as `volume.*`.

## Host and system

`GET /api/system`

```json
{"docker":{"ServerVersion":"27.3.1","OSType":"linux","Architecture":"aarch64",
           "NCPU":4,"MemTotal":8261398528,"ContainersRunning":6,"CgroupVersion":"2",
           "Driver":"overlay2","Name":"nuc"},
 "docker_version":{"ApiVersion":"1.47"},
 "endpoint":"unix:///var/run/docker.sock",
 "managed_network":{"name":"docman0","subnet":"172.20.0.0/16","…":"…"},
 "metrics_available":true,
 "docman":{"version":"1.0","platform":"linux/arm64","data_dir":"/data","tls":true}}
```

`GET /api/system/metrics` returns one host sample. It is taken from procfs, so
`metrics_available:false` means the engine is remote or not Linux.

```json
{"t":1736200000123,"ncpu":4,
 "cpu_percent":18.4,"cpu_user":11.2,"cpu_system":5.1,"cpu_iowait":1.4,"cpu_steal":0,
 "per_cpu_percent":[22.1,14.0,19.8,17.7],
 "memory":{"total":8261398528,"available":4102000000,"cached":2100000000,
           "swap_total":0,"swap_free":0},
 "mem_used":4159398528,"swap_used":0,
 "disk_read_bytes":524288,"disk_write_bytes":1048576,
 "disk_read_ops":12,"disk_write_ops":34,
 "disks":[{"name":"nvme0n1","read_bytes":524288,"write_bytes":1048576}],
 "load1":0.42,"load5":0.55,"load15":0.61,"uptime":864000}
```

## WebSocket streams

Connect to `wss://<host>/api/stream/…`. Browsers send the session cookie;
scripts append `?access_token=<bearer token>`. Every message is JSON text with a
`type` field, except console output, which is binary.

### `/api/stream/host` — host metrics with a container breakdown

Every `stats_interval_ms` (default 2000):

```json
{"type":"host","host":{…host sample…},"containers":[{…container sample…}]}
```

### `/api/stream/containers` — the container list, live

```json
{"type":"containers","containers":[…list shape…],"stats":[…container samples…]}
```

### `/api/stream/stats/{id}` — one container

```json
{"type":"stats","sample":{…container sample…}}
```

### `/api/stream/logs/{id}` — live logs

Query: `tail` (number or `all`), `timestamps=1`, `stdout=0`, `stderr=0`.

```json
{"type":"log","stream":1,"data":"listening on :80\n"}
```

`stream` is 1 for stdout, 2 for stderr. Historic lines arrive first, then the
stream follows.

### `/api/stream/exec/{id}` — interactive console

Query: `cmd` (run through `sh -c`), `shell` (exact binary), `user`, `workdir`,
`cols`, `rows`. With neither `cmd` nor `shell`, DocMan runs bash when the image
has it and sh otherwise.

Server → client:

- **binary frames**: raw terminal output, TTY enabled.
- `{"type":"ready","exec_id":"…"}` once attached.
- `{"type":"exit","code":0}` when the command finishes.
- `{"type":"error","error":"…"}` if it could not start.

Client → server (text frames):

```json
{"type":"in","data":"ls -la\r"}
{"type":"resize","cols":120,"rows":40}
{"type":"eof"}
{"type":"close"}
```

Binary frames sent by the client are written to stdin verbatim.

### `/api/stream/events` — docker's own event stream

```json
{"type":"event","event":{"Type":"container","Action":"start","Actor":{…}}}
```

## Access management

DocMan has any number of user accounts. Every account is a full administrator;
what each has of its own is how it signs in — a password, passkeys and an
authenticator app — and its sessions. The two-passkey rule in
[How sign-in is allowed](/help/api/auth) applies to each account separately.

API tokens belong to DocMan, not to an account. A token has full rights,
including managing users, and keeps working if the user who created it is
renamed or removed. It has no account of its own, so the `/api/account/*`,
`/api/passkeys*` and TOTP endpoints answer a token with `403`: those change the
signed-in user's own sign-in and need a browser session.

### Users

```
GET    /api/users                      # every account and how it can sign in
POST   /api/users                      {"username":"jsmith","password":"…"}  → 201 {user}
POST   /api/users/{id}/reset           {"password":"…"}
DELETE /api/users/{id}
```

```json
{"users":[{"id":2,"username":"jsmith","created_at":1736200000,"last_login_at":1736210000,
           "has_password":true,"password_enabled":true,"totp_enabled":false,
           "passkey_count":0,"current":false}],"passkey_target":2}
```

- `password_enabled` is whether that account can sign in with its password now.
- Usernames are 3 to 32 characters of letters, digits, `.`, `-` and `_`, and
  must be unique ignoring case. Passwords follow the same rules as everywhere
  else: at least 12 characters, mixing letters with digits or symbols.
- `reset` is for recovering someone who is locked out: it sets the new password
  and removes that account's passkeys, authenticator app and sessions in one
  step. It refuses the caller's own account (`409`).
- `DELETE` removes the account with its passkeys and sessions. It refuses the
  caller's own account and the last remaining one (`409`). Audit entries and
  API tokens the account created are kept.

### Your own account

These act on the signed-in user and need a browser session.

```
GET    /api/account                    # username, passkey count, whether password sign-in is on
POST   /api/account/password           {"current":"…","new":"…"}
POST   /api/account/username           {"username":"ops"}
GET    /api/account/sessions
POST   /api/account/sessions/revoke    # end every other browser session

GET    /api/passkeys                   # {passkeys:[…], password_enabled, target}
POST   /api/passkeys/register/begin    # → {challenge_id, options}
POST   /api/passkeys/register/finish   {"challenge_id":"…","name":"Laptop","response":{…}}
PATCH  /api/passkeys/{id}              {"name":"Yubikey"}
DELETE /api/passkeys/{id}

POST   /api/account/totp/begin         # → {secret, secret_formatted, uri, qr, digits, period}
POST   /api/account/totp/enable        {"code":"123456"}
POST   /api/account/totp/disable       {"password":"…"} or {"code":"123456"}
```

### API tokens

```
GET    /api/tokens
POST   /api/tokens                     {"name":"ci","expires_in_days":90}  → {token, record}
DELETE /api/tokens/{id}?purge=0        # revoke; purge=1 deletes the record
```

`POST /api/tokens` is the only place a token secret is ever returned. Every user
sees and can revoke every token. Each record says who created it:

```json
{"tokens":[{"id":7,"name":"ci","prefix":"ab12cd34","scope":"admin",
            "created_at":1736200000,"last_used_at":1736290000,"expires_at":0,"revoked":false,
            "created_by_id":2,"created_by":"jsmith"}]}
```

- `created_by` is the creator's current username. When that account has been
  removed it is the name recorded at creation, with `"created_by_removed":true`.
- A token created with another token has `created_by_id` 0 and `created_by`
  `token:<name>`.
- Tokens from before DocMan 1.5 have an empty `created_by`.
- `last_used_at` is updated on every request the token makes, `0` if never.

### Authenticator app (TOTP)

Standard RFC 6238: HMAC-SHA1, six digits, thirty second step, one step of clock
skew accepted either side.

`begin` returns the secret three ways so any app can take it — `qr` is a PNG
data URI, `uri` is the `otpauth://` link, and `secret_formatted` is the base32
secret in groups of four for typing by hand. The secret is held in memory only;
it reaches the database when `enable` succeeds, so an abandoned enrolment cannot
lock anyone out. Enrolment is for the signed-in user, so it uses a browser
session (sign in first to get the cookie) rather than a token:

```bash
curl -sk -c jar -H 'Content-Type: application/json' -H 'X-DocMan-CSRF: 1' \
  -d '{"username":"jsmith","password":"…"}' $D/api/auth/login
curl -sk -b jar -X POST -H 'X-DocMan-CSRF: 1' $D/api/account/totp/begin | jq -r .uri
curl -sk -b jar -X POST -H 'X-DocMan-CSRF: 1' -H 'Content-Type: application/json' \
  -d '{"code":"123456"}' $D/api/account/totp/enable
```

Signing in once it is enrolled:

```bash
curl -sk -X POST -H 'Content-Type: application/json' -H 'X-DocMan-CSRF: 1' \
  -d '{"username":"admin","password":"…","code":"123456"}' $D/api/auth/login
```

Omitting the code returns `401` with `"totp_required": true`, which is how the
UI knows to show the field. A wrong password and a wrong code give the same
generic error, so the endpoint cannot be used to test passwords on their own.
Each code is accepted once: the step it came from is recorded, and codes from
that step or earlier are refused.

## Registries

`GET /api/registries` →

```json
{"registries":[{"id":1,"name":"Docker Hub","kind":"dockerhub","host":"docker.io",
  "auth_type":"none","username":"","is_default":true,"has_secret":false,"options":{},
  "created_at":1790000000,"updated_at":1790000000}],
 "kinds":[{"kind":"ghcr","name":"GitHub Container Registry","host":"ghcr.io",
  "auth":["basic","none"],"auth_help":"…","search":true,"search_note":"…"}]}
```

Docker Hub is added as the default the first time the list is read. Secrets are
never returned; `has_secret` says whether one is saved.

`POST /api/registries` and `PUT /api/registries/{id}`:

```json
{"kind":"ecr","name":"AWS prod","host":"123456789012.dkr.ecr.eu-west-1.amazonaws.com",
 "auth_type":"aws","username":"AKIA…","secret":"…","options":{"region":"eu-west-1"}}
```

| Field | Notes |
| --- | --- |
| `kind` | `dockerhub` `ghcr` `ghes` `gitlab` `quay` `ecr` `gar` `acr` `github-repo` `other` |
| `name` | up to 60 characters; the kind's name when empty |
| `host` | the registry address; the kind's usual one when empty. A scheme or path is stripped |
| `auth_type` | `none`, `basic` (username + password/token), `token` (identity token in `secret`), `aws` (access key ID in `username`, secret key in `secret`), `gcp` (service-account JSON in `secret`); each kind allows some of these (`auth` in the kinds list) |
| `secret` | on `PUT`, `null` keeps the saved one |
| `options` | `region` for `ecr` (read from the host when empty); `api_url` for `ghes`; for `github-repo`, `repo` (required: `owner/name` or its URL), `ref` (branch or tag), `path` (folder) and `api_url` |

`DELETE /api/registries/{id}` refuses the default registry (409).
`POST /api/registries/{id}/default` makes it the default.
`POST /api/registries/{id}/test` → `{"ok":true,"message":"Login Succeeded"}`; a
failure is `ok:false` with the reason, still status 200.

`GET /api/registries/{id}/search?q=post` →
`{"results":[{"name":"postgres","description":"…","stars":15000,"official":true}]}`
(at most 50). `name` is the path within the registry. Registries that cannot be
searched (GitLab, Google) answer 502 with the reason.

`GET /api/registries/{id}/tags?repo=home-assistant/home-assistant` →
`{"tags":["latest","2026.9.3","2026.9.2",…]}` (at most 300): `latest` first, then
versions newest first; signature tags (`sha256-….sig`) are left out. 404 when the
registry has no such image.

### GitHub repositories

A `github-repo` registry holds pre-built image archives (`docker save`, as
`.tar`, `.tar.gz` or `.tgz`) in its files or releases. It cannot be searched or
pulled from; list and install instead.

`GET /api/registries/{id}/archives` →

```json
{"repo":"mwareman/docman","ref":"main","host_arch":"amd64",
 "artifacts":[{"key":"asset:1234","name":"docman","version":"1.9.2","arch":"amd64","fits":true,
   "source":"release","release":"v1.9.2","file":"v1.9.2/docman-1.9.2-amd64.tar.gz","size":5838661}],
 "build_files":["Dockerfile","docker-compose.yml"]}
```

Newest first within each `name`. `fits` is false for archives of another
architecture. `build_files` lists Dockerfiles and compose files, which DocMan does
not build.

`POST /api/registries/{id}/install` `{"key":"asset:1234"}` streams
`application/x-ndjson`: `{"status":"…"}` lines, then `{"loaded":["docman:1.9.2","docman:latest"]}`,
or `{"error":"…"}`.

References installed this way have `origin: "repository"` in `GET /api/images`.
`POST /api/images/pull` for one of them installs the repository's newest archive
of it instead of pulling, and a recreate with `pull:true` does the same.

## Settings

`GET /api/settings` → `{"settings":{…},"managed_network_name":"docman0","managed_network_subnet":"172.20.0.0/16"}`

`PATCH /api/settings` accepts any subset of:

| Key | Values | Effect |
| --- | --- | --- |
| `theme` | `system` `dark` `light` | UI theme |
| `log_tail` | number | lines the log view loads |
| `stats_interval_ms` | 500–30000 | live refresh interval |
| `rp_id` | hostname | hostname passkeys bind to; changing it invalidates them |
| `confirm_destructive` | `true` `false` | ask before destructive actions |

## Audit

`GET /api/audit?limit=200`

```json
{"entries":[{"id":812,"at":1736200000,"actor":"token:ci","action":"container.update",
             "target":"web","detail":"configuration applied by recreating the container","ok":true}]}
```

Actions are namespaced: `container.*`, `image.*`, `volume.*`, `address.*`,
`token.*`, `user.*`, `account.*`, `passkey.*`, `totp.*`, `auth.*`, `settings.*`,
`bootstrap.*`. `actor` is who made the change: the signed-in username, or
`token:<token name>` for every change made with an API token, whichever user
created it. Sign-in attempts are recorded under the username that was tried.

## Recipes

Update a container to the newest image, and wait for the job to finish:

```bash
job=$(curl -sk -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"pull":true}' $D/api/containers/web/recreate | jq -r .job.id)
until [ "$(curl -sk -H "Authorization: Bearer $T" $D/api/jobs/$job | jq -r .state)" != running ]; do sleep 2; done
```

Copy a file out of a volume, and put an edited one back:

```bash
curl -sk -H "Authorization: Bearer $T" -o app.yml "$D/api/volumes/web-data/files/download?path=/config/app.yml"
curl -sk -X POST -H "Authorization: Bearer $T" --data-binary @app.yml \
  "$D/api/volumes/web-data/files/upload?path=/config&name=app.yml&overwrite=1"
```

Change one environment variable, keeping everything else:

```bash
spec=$(curl -sk -H "Authorization: Bearer $T" $D/api/containers/web/spec | jq '.spec')
spec=$(echo "$spec" | jq '.env = ((.env | map(select(startswith("LOG_LEVEL=") | not))) + ["LOG_LEVEL=debug"])')
curl -sk -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"spec\":$spec,\"start\":true}" $D/api/containers/web/spec
```

Deploy an image with everything it needs worked out automatically:

```bash
plan=$(curl -sk -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"image":"redis:7-alpine"}' $D/api/images/deploy-plan)
spec=$(echo "$plan" | jq '.spec')
curl -sk -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"spec\":$spec,\"start\":true,\"pin_ip\":true}" $D/api/containers
```

Read the last 200 log lines of a failing container, then look at why it stopped:

```bash
curl -sk -H "Authorization: Bearer $T" "$D/api/containers/web/logs?tail=200" | jq -r .logs
curl -sk -H "Authorization: Bearer $T" $D/api/containers/web/inspect | jq '.State'
```

Find which container holds a volume before deleting it:

```bash
curl -sk -H "Authorization: Bearer $T" $D/api/volumes | jq '.volumes[] | select(.Name=="web-data") | .used_by'
```

Run a command inside a container from a script (Python, using `websockets`):

```python
import asyncio, json, websockets
async def main():
    url = "wss://docman.local:9444/api/stream/exec/web?cmd=cat%20/etc/hostname&access_token=" + TOKEN
    async with websockets.connect(url, ssl=NO_VERIFY) as ws:
        async for message in ws:
            print(message if isinstance(message, str) else message.decode(errors="replace"), end="")
asyncio.run(main())
```
