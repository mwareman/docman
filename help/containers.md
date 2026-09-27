# The container list

**Containers** lists every container on the host, running or not, and keeps the list up to date by itself as containers start, stop and change. It is the starting point for almost everything you do with a container.

## Reading the list

| Column | What it shows |
| --- | --- |
| **Container** | A coloured dot for its state, its name and the first 12 characters of its ID. DocMan's own container carries a **DocMan** badge. |
| **Image** | The image the container was created from. |
| **Address** | Its internal IP address. A highlighted address is a [fixed address](/help/addresses). The network names are listed underneath. |
| **Ports** | Host ports published to the container, as `host→container`. Each one is a link that opens that port on this host in a new tab. Up to four are shown. |
| **CPU** and **Memory** | Live usage, for running containers. |
| **State** | Docker's status text, such as `Up 3 hours (healthy)` or `Exited (0) 2 minutes ago`. |

The dot is green for running, red for a container that exited or died, and amber for anything in between, such as paused, restarting or created but never started.

### Update available

An **Update available** badge beside a container's state means a newer version of its image exists and the container is still running the old one. There are two cases, and the tooltip says which:

- **On this host:** a newer image arrived under the tag the container was created from, through **Images → Upload image**, `docker load` on the host, or a pull.
- **In the registry:** the daily registry check found a newer version of the container's image that has not been pulled yet.

Click the badge to open the container. Choose **Recreate**, on the container's row or its page, to switch it to the new version; for a registry update the newer image is pulled first. The badge then disappears. See [Updating containers](/help/updating).

The badge is not shown when an *older* image is tagged back under the same name, or for containers created from a fixed image ID or digest, which cannot change.

## Finding a container

- Type in the **Filter** box to match on name, image, IP address or port number.
- Use **All**, **Running** or **Stopped** to narrow the list by state.
- Click anywhere on a row to open the container's own page. See [Working with a container](/help/container).

## Quick actions

The buttons at the end of each row act on that container immediately:

| Button | Action |
| --- | --- |
| **Start** / **Stop** | Start a stopped container, or stop a running one. Stop asks the main process to exit, then kills it after 10 seconds. |
| **Resume** | Shown instead of Start for a paused container. |
| **Restart** | Stop and start again. Only for running containers. |
| **Recreate** | Rebuild the container from its image, pulling the newest version first when the image came from a registry. Highlighted when an update is available. See [Recreate](/help/container#recreate). |
| **Inspect** | Show the container's complete Docker configuration as JSON. See [Inspect](/help/container#inspect). |
| **Logs** | Open the container's [Logs](/help/logs) tab. |
| **Console** | Open a [shell inside the container](/help/console). Only for running containers. |
| **Remove** | Delete the container, after confirmation. |

## Removing a container

**Remove** stops the container if it is running and deletes it. You are asked to confirm first.

- **Named volumes are always kept**, so the data survives and can be attached to a new container.
- Tick **Also delete this container's anonymous volumes** to delete the unnamed volumes that were created just for it, including ones it kept across a recreate. A volume another container also uses is left alone. Anything stored only there is lost.
- A container's [fixed address](/help/addresses) is released when it is removed, so the address becomes free for another container. To replace a container but keep its address, use **Recreate** instead. See [Updating containers](/help/updating).

> **Warning:** Anything the container wrote to its own filesystem, outside a volume, is deleted with it. This cannot be undone.

## Creating a container

**New container** opens the deploy wizard. See [Deploying a container](/help/deploy).
