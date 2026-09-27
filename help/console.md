# The console

The **Console** tab opens an interactive terminal inside a running container, like `docker exec -it`. Use it to look around the container's filesystem, run a one-off command, or check how the application sees its environment.

## Connecting

The console connects as soon as you open the tab. By default it starts `bash` if the container has it, and `sh` otherwise, as the container's default user.

To run something else, set the fields in the toolbar and choose **Connect** (or **Reconnect**):

| Field | What it does |
| --- | --- |
| **Command** | The program to start instead of a shell, for example `psql -U postgres` or `redis-cli`. It is run through `/bin/sh`, so pipes, `&&` and environment variables such as `$HOME` work. Leave it empty for the automatic shell. |
| **User** | Who to run it as: a user name, a numeric ID such as `1000`, or `user:group`. Leave it empty for the container's default user. Use `root` when you need to install or change something the application's own user cannot. |

The status at the end of the toolbar reads **connected** while the session is open. When the program exits it shows the exit status, for example **exited (0)**, and you can **Reconnect** to start a new session.

## Using the terminal

- It behaves like a normal terminal: full-screen programs such as `top`, `less` and `vi` work, and colours are shown.
- The terminal resizes itself to fit the window.
- **Clear** clears the screen.
- Select text with the mouse to copy it; paste with your usual shortcut.

## When it will not connect

- **The container is not running.** A console needs a running container. Start it first. If it stops immediately, read its [Logs](/help/logs) instead.
- **No shell in the image.** The console needs `/bin/sh` inside the container, even for a custom command. Minimal images, such as those built `FROM scratch` or distroless images, have none, so the console cannot open. DocMan's own image is one of these. Use [Logs](/help/logs) and [Statistics](/help/statistics) to look into such containers instead.
- **The user does not exist.** Use a numeric ID, or leave **User** empty.

> **Warning:** Changes you make inside a container through the console are lost when the container is recreated, unless they are inside a volume. Make lasting changes through the container's [configuration](/help/configuration) or its image instead.
