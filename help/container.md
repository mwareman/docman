# Working with a container

Click a container in the list to open its page. The name and current state are at the top, with the most common actions beside them, and five tabs underneath:

| Tab | What it is for |
| --- | --- |
| **Overview** | What the container is, its network and addresses, and the lifecycle actions. This page. |
| **Logs** | Its output, live. See [Logs](/help/logs). |
| **Statistics** | Live CPU, memory, disk and network use, and its processes. See [Statistics and processes](/help/statistics). |
| **Console** | A shell inside the running container. See [The console](/help/console). |
| **Configuration** | Everything about how it runs, editable. See [Changing configuration](/help/configuration). |

The address of each tab includes the container's ID, so you can bookmark a tab or share a link to it. When a container is recreated its ID changes, and DocMan moves the page to the new ID for you.

## Starting, stopping and pausing

The buttons at the top of the page depend on the container's state:

| State | Buttons |
| --- | --- |
| Stopped (exited or created) | **Start** |
| Running | **Stop**, **Restart**, **Pause** |
| Paused | **Resume**, **Stop** |

- **Stop** asks the container's main process to exit, using the image's stop signal (usually `SIGTERM`), and kills it if it is still running after 10 seconds.
- **Restart** is a stop followed by a start.
- **Pause** freezes every process in the container without stopping them. Memory is kept and nothing runs until you **Resume**. That is useful for taking a consistent copy of a volume without shutting an application down.

## Inspect

**Inspect**, at the top of the page and on the container's row in the list, shows everything Docker holds about the container, exactly as `docker inspect` reports it: its configuration, state, mounts, networks and more.

- **Tree** shows it as a collapsible outline. Objects and lists show how many entries they hold; click to open one. **Expand all** and **Collapse all** open or close every level.
- **Raw JSON** shows the whole document as text.
- **Find** highlights every key and value that contains your text, opens the parts of the tree that hold a match, and says how many there are.
- **Copy** puts the JSON on the clipboard; **Download** saves it as a `.json` file.

> **Note:** Inspect shows environment variables in full, including any passwords set there.

## The Container card

| Field | Meaning |
| --- | --- |
| **ID** | The start of the container's ID. The copy button copies the full ID. |
| **Image** | The image reference it was created from, with the short ID of the exact image version underneath. |
| **Command** | The entrypoint and command it runs. |
| **Created** / **Started** | When the container was created, and when it last started. |
| **Restart policy** | What Docker does when the container stops. See [Changing configuration](/help/configuration#restart-policy). |
| **Restarts** | How many times Docker has restarted it automatically. A number that keeps growing means it is crashing and being brought back. |
| **Exit code** | For a stopped container, the code its main process exited with. `0` means a clean exit; `137` means it was killed, often for running out of memory; other values are specific to the application. |
| **User** / **Working dir** | The user it runs as and its starting directory. |
| **Log driver** | Where Docker sends the container's output. The Logs tab reads Docker's local copy, so a driver that keeps none leaves it empty. |
| **Health** | For images with a health check: `healthy`, `starting` or `unhealthy`, with the number of consecutive failures. |

Underneath are the container's **Storage**, meaning each volume, host path or tmpfs and where it is mounted, marked **ro** when read-only, and up to twelve of its **Labels**.

## The Network card

- Each network the container is attached to, with its address on that network. A highlighted address is fixed.
- **Published** lists the host ports that lead to it. Click one to open it in a new tab.
- **Fix this address** gives the container an internal address that never changes; **Release fixed address** undoes it. See [Fixed addresses](/help/addresses).

## Lifecycle actions

The **Lifecycle** card at the bottom of the Overview tab holds the less frequent, heavier actions.

### Recreate

Replaces the container with a new one built from the same configuration. Use it to move to a newer version of the image, or to get a clean container filesystem. It is also on the container's row in the container list.

**Pull the newest version of the image first** is set for you from where the image came from:

- **From a registry:** ticked, so a newer version in the registry is fetched before rebuilding. When the daily check has found one, the dialog says so.
- **Uploaded as an archive, or built on the host:** unticked, because there is no registry to pull from. The version on this host is used.

You can change the tick either way before confirming.

#### Recreate runs on the server

The pull and rebuild run as a job on DocMan itself, not in your browser. So they finish even if your browser loses its connection. That is exactly what happens when the container being upgraded is the one you reach DocMan through, such as a Cloudflare tunnel or a reverse proxy.

- A notice at the bottom of the page follows the job: pulling, stopping, creating the replacement, starting.
- If the connection drops, it says so and keeps trying to reconnect. The job carries on regardless.
- When the job ends, the page reports the result and moves to the container's new ID. If you reload the page while a job is running, it picks the job up again and still reports how it ended.
- The container comes back in the state it was in: running, stopped, or paused.
- A link or bookmark to the container's old ID takes you to the new one.

Applying a configuration change works the same way.

Named volumes and any fixed address carry over. Anything the container wrote to its own filesystem outside a volume does not. [Updating containers](/help/updating) covers this in detail, including images that were uploaded rather than pulled.

### Rename

Gives the container a new name: letters, digits, dot, dash and underscore. The container keeps running, and a fixed address follows it to the new name.

### Kill

Sends `SIGKILL`, which ends the main process immediately without letting it shut down cleanly. Use it only when **Stop** does not work: a killed database, for example, may have to recover its files on next start.

### Remove

Stops and deletes the container, after confirmation. Named volumes are kept; tick **Also delete anonymous volumes** to delete the unnamed ones created just for it. Its fixed address is released. See [Removing a container](/help/containers#removing-a-container).

## DocMan's own container

DocMan can manage itself. Its row in the container list carries a **DocMan** badge.

- **Restart** works as for any container. The page reconnects once DocMan is back.
- **Recreate**, and applying a configuration change, are handed to a short-lived helper container. DocMan cannot stop itself and then start its replacement, so the helper does the swap. The page tells you DocMan is recreating itself and reloads when the new instance answers, usually within about fifteen seconds. If the swap fails, the original DocMan is put back and started again, and the helper's log is copied into DocMan's own log.
- **Stop** and **Remove** work, but you then have to start DocMan again from the host, because DocMan is no longer running.
