# DocMan help

DocMan manages the containers on one Docker host: the host it runs on. This guide explains every part of the application, from deploying your first container to keeping it up to date, plus a complete reference for the REST API.

> **Tip:** Press `/` anywhere in the help to search it. Every page is also listed in the menu on the left.

## Where to start

- New to DocMan? Read [Setting up DocMan](/help/getting-started) to sign in for the first time and secure your account.
- Want to run something? [Deploying a container](/help/deploy) walks through the New container wizard.
- Need to change a running container? See [Changing configuration](/help/configuration).
- Moving to a new version of an image? See [Updating containers](/help/updating).
- Automating DocMan from a script? Start with the [API reference](/help/api).

## Finding your way around

The menu on the left of DocMan has one entry per area:

| Menu entry | What it is for | Guide |
| --- | --- | --- |
| **Overview** | Live host CPU, memory and disk figures, broken down by container | [The Overview page](/help/overview) |
| **Containers** | Every container on the host, with quick actions | [The container list](/help/containers) |
| **Images** | Images on the host: pull, upload, tag, inspect, remove | [Images](/help/images) |
| **Volumes** | Persistent storage and which containers use it | [Volumes](/help/volumes) |
| **Addresses** | Fixed internal IP addresses for containers | [Fixed addresses](/help/addresses) |
| **Settings** | Your account and passkeys, other users, API tokens, preferences and the activity log | [Settings](/help/settings) |
| **Help** | This guide, which opens in a new tab | |

The number beside Containers, Images and Volumes is how many of each exist on the host.

## Common tasks

| I want to… | Where |
| --- | --- |
| Run a new container from an image | **Containers → New container**, see [Deploying a container](/help/deploy) |
| Start, stop or restart a container | The buttons on its row in **Containers**, or at the top of its page |
| Read a container's output | Open the container, then the **Logs** tab, see [Logs](/help/logs) |
| Get a shell inside a container | Open the container, then the **Console** tab, see [The console](/help/console) |
| Change environment variables, ports or storage | Open the container, then the **Configuration** tab |
| Move a container to a newer image | **Recreate** on the container's Overview tab, see [Updating containers](/help/updating) |
| Load an image on a host with no registry access | **Images → Upload image**, see [Images](/help/images#uploading-an-image-archive) |
| Give a container an IP address that never changes | **Fix this address** on the container's Overview tab |
| Give someone else their own sign-in | **Settings → Users → Add a user**, see [Users](/help/settings#users) |
| Let a locked-out colleague back in | **Settings → Users → Reset sign-in** |
| See who changed what | **Settings → Activity** |
| Script DocMan | **Settings → API tokens**, then the [API reference](/help/api) |

## How DocMan relates to Docker

DocMan is a front end for the Docker Engine on its own host. Everything you see is read live from Docker, and everything you do is carried out by Docker. So:

- Containers, images and volumes you create with the `docker` command appear in DocMan straight away, and the reverse.
- DocMan keeps only a little state of its own: user accounts and their passkeys, API tokens, fixed addresses, preferences and the activity log. It lives in DocMan's `/data` volume.
- DocMan runs in a container itself. It is marked with a **DocMan** badge in the container list, and it can safely restart, recreate and update itself.

> **Warning:** DocMan holds the Docker socket, which gives it full control of the host. Anyone who can sign in to DocMan can do anything on the host that root can. Protect access to it as you would protect a root shell.

## Conventions in this guide

- Names of buttons, tabs and fields are in **bold**, for example **Save and start**.
- A path such as **Settings → API tokens** means "open Settings, then the API tokens tab".
- `Monospace text` is something you type, or a value DocMan shows.
