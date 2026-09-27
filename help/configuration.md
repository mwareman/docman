# Changing configuration

The **Configuration** tab shows everything about how a container runs and lets you change it: environment, ports, storage, limits, command and more. The same form appears in the [deploy wizard](/help/deploy) when you create a container.

## How changes are applied

Docker cannot change a container's environment, command, ports or storage while it exists. So when you choose **Apply changes**, DocMan replaces the container with a new one:

1. It reads the container's complete Docker configuration, including settings the form does not show, so those carry over untouched.
2. It applies your edits on top.
3. It stops the container and renames it aside.
4. It creates the replacement under the original name, reattaches every network and reapplies any fixed address.
5. It deletes the original. If step 4 fails, it renames the original back and restarts it instead, so a failed change never loses the container.

What this means for you:

- **Kept:** the name, named volumes and host-path mounts with their data, the fixed address, networks, and every setting you did not change.
- **Lost:** anything the container wrote to its own filesystem outside a volume, and the running processes. The container restarts from scratch.
- **Changed:** the container's ID. DocMan moves the page to the new ID for you.

Tick **Start after applying** to start the replacement straight away. It starts ticked if the container was running; left as it is, the container comes back exactly as it was, paused included. **Discard** throws your edits away and returns to the Overview.

Like **Recreate**, the change runs as a job on DocMan itself, so it completes even if your browser loses its connection part-way. See [Recreate runs on the server](/help/container#recreate-runs-on-the-server).

> **Note:** DocMan checks the form before sending it. Relative mount paths, two mounts on the same path, and one host port used twice are reported under the form, and nothing changes until they are fixed.

## The configuration form

### Name, image and restart policy

| Field | Meaning |
| --- | --- |
| **Name** | The container's name. On the Configuration tab it is locked; use **Rename** on the Overview tab instead. |
| **Image** | The image reference to run, such as `nginx:1.27-alpine`. Changing it here moves the container to a different image or tag. |
| **Restart policy** | What Docker does when the container stops. See below. |

### Restart policy

| Policy | Behaviour |
| --- | --- |
| **Unless stopped** | Restart after a crash and when Docker starts, for example after a reboot, but stay down after you stop it. The right choice for almost every service. |
| **Always** | As above, and also restart after you stop it, once Docker itself restarts. |
| **On failure** | Restart only when the process exits with a non-zero code, up to 5 times. For jobs that should run to completion. |
| **Never** | Leave it stopped, whatever happens. |

> **Warning:** Restart policies only bring containers back after a reboot if the Docker service itself starts at boot. On most Linux hosts that means `sudo systemctl enable docker`.

### Environment

One row per variable: a **KEY** and its value.

- **Add variable** adds an empty row; the ✕ button removes one.
- Values whose names look secret are hidden. Use the key button beside them to show the value.
- **Paste .env** opens a box where you can paste the contents of a `.env` file. Choose **Import** to turn every `KEY=value` line into a row. Blank lines and lines starting with `#` are skipped, and quotes around values are removed.

### Published ports

Each row makes a port inside the container reachable from outside the host.

| Column | Meaning |
| --- | --- |
| **Container** | The port the application listens on inside the container, such as `80`. |
| **Host** | The port on the host that leads to it, such as `8080`. Leave it empty and Docker picks a free port each time the container starts. |
| **Protocol** | `tcp`, `udp` or `sctp`. |
| **Bind address** | Which of the host's addresses to listen on. Leave it empty for all of them; use `127.0.0.1` to reach the container only from the host itself. |

Containers on the same Docker network can reach each other's ports without publishing anything. Publish a port only when something outside Docker needs it.

### Storage

Each row mounts storage into the container.

| Kind | Source | Use it for |
| --- | --- | --- |
| **Volume** | Chosen from a list: one of the volumes on this host, an **Anonymous volume** made just for this container (an existing one keeps its data when the container is rebuilt), or **New volume…** with a name you type, which is created when you save. A volume named in the settings that does not exist yet is listed under **Not created yet**. | Data the container must keep, such as a database. The usual choice. |
| **Host path** | An absolute path on the host, such as `/srv/app/config`. | Sharing files with the host, such as configuration you edit there. |
| **tmpfs** | None. | Scratch space held in memory and emptied on every restart. |

**Container path** is where it appears inside the container, and must start with `/`. Tick **ro** to make it read-only inside the container.

### Advanced options

Open **Advanced options** for everything else:

| Field | Meaning |
| --- | --- |
| **Entrypoint** | Replaces the image's entrypoint, the program that always runs. Leave it empty to use the image's. Quote arguments containing spaces. |
| **Command** | Replaces the arguments passed to the entrypoint. Leave it empty to use the image's. |
| **User** | Who the main process runs as: a name, a numeric ID or `uid:gid`, such as `1000:1000`. |
| **Working directory** | The directory the process starts in. |
| **Hostname** | The container's own host name. Leave it empty for Docker's default, the start of the container ID. |
| **Network** | Which network the container joins, chosen from the networks that exist on this host. See [Network](#network) below. |
| **Memory limit (MB)** | The most memory the container may use. It is stopped if it uses more. Leave it empty for no limit. |
| **CPU limit (cores)** | How much CPU time it may use, in cores. `0.5` is half of one core. |
| **Process limit** | The most processes it may run at once. It guards the host against runaway process creation. |
| **DNS servers** | Name servers to use instead of the host's, comma-separated. |
| **Extra hosts** | Extra entries for the container's `/etc/hosts`, as `name:address`, comma-separated. |
| **Labels** | Docker labels, one `key=value` per line. |

The switches underneath:

| Switch | Meaning |
| --- | --- |
| **Allocate a TTY** | Give the main process a terminal. Some interactive programs need one. |
| **Keep stdin open** | Keep standard input open even when nothing is attached. |
| **Remove automatically when it exits** | Docker deletes the container as soon as it stops. For one-off jobs only: a restart policy has no effect, and the logs are lost with it. |
| **Read-only root filesystem** | The container cannot write anywhere except its volumes and tmpfs mounts. A good hardening step for applications that allow it. |
| **Privileged** | Full access to the host's devices and kernel, with none of the usual isolation. Use it only when you trust the image completely and it genuinely needs it. |

### Network

The **Network** list offers only what exists on this host, so a container can never be pointed at a network with a typing mistake in its name:

| Group | Choices |
| --- | --- |
| **Docker networks** | `bridge`, Docker's default network, and every network created on the host, with its driver and subnet. DocMan's own `docman0` is marked **DocMan fixed addresses**. |
| **Special** | `host` shares the host's network directly, with no isolation and no published ports needed. `none` gives the container no networking at all. |
| **Share another container's network** | Join another container's network namespace, so both share one address and can reach each other on `localhost`. |

If the container's current setting no longer exists, for example its network was deleted, it appears at the top under **Current setting**, marked as missing. It stays selected, so opening and saving the form never changes it behind your back, but the container will not start on it until you choose a network that exists.

A container with a [fixed address](/help/addresses) always runs on DocMan's own network, so the list is locked. Release the address first to move it elsewhere.

### Capabilities

Linux capabilities are the individual powers root normally has, such as changing file ownership or configuring network interfaces. Docker gives every container a limited set of them by default, and the **Capabilities** checklist shows exactly which ones this container has:

- A **ticked** box means the container has that capability; untick it to take it away, tick another to grant it.
- **Hover over a capability** to see what it allows.
- **Granted by Docker by default** lists the 14 every container gets unless told otherwise. **Off unless added** lists the rest.
- Capabilities marked **!** weaken the isolation between the container and the host, such as `SYS_ADMIN`, `NET_ADMIN` or `SYS_TIME`. Grant them only when the application's documentation asks for them.
- The summary beside the heading says how the choice differs from Docker's defaults. **Docker defaults** puts every box back to the default set.
- With **Privileged** switched on, the container gets every capability whatever is ticked, so the checklist is greyed out.

> **Tip:** Removing capabilities an application does not need is a cheap hardening step. Many services run fine with `NET_RAW`, `MKNOD` and `AUDIT_WRITE` unticked.

Existing containers keep their settings exactly: DocMan works out what a container has from how it was created, including settings such as "drop all capabilities, then add one", and writes them back unchanged unless you tick or untick something.

### Limits

Memory, CPU and process limits protect the host, and the other containers, from one container that misbehaves. A container that reaches its memory limit is stopped by the kernel, which shows up as exit code `137` and, with a restart policy, as a growing **Restarts** count.

## Changing only the image version

To move to a newer image under the same tag, you do not need the Configuration tab. Use **Recreate** on the Overview tab, as described in [Updating containers](/help/updating).
