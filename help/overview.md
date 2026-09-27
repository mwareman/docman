# The Overview page

**Overview** is the first page you see after signing in. It shows what the whole host is doing, live, and which containers are responsible. Figures refresh every two seconds by default; you can change that in **Settings → Preferences → Statistics interval**.

## The four tiles

| Tile | What it shows |
| --- | --- |
| **Host CPU** | Total CPU use across all cores, with a gauge and a short history. The line underneath splits it into user, system and I/O wait time. |
| **Memory** | Memory in use out of the host's total, with the share as a bar that turns amber above 75% and red above 90%. Also shows the page cache and swap, when the host has swap. |
| **Disk I/O** | Combined read and write throughput across the host's disks, with a history of each and the number of operations per second. |
| **Containers** | How many containers exist and how many are running, with a link to the container list. |

## Container share of the host

Two stacked bars show how the host's CPU and memory are divided between the ten busiest running containers. The grey remainder of the memory bar is everything else: the host itself, other processes and cache.

The table underneath lists every running container, busiest first, with its CPU, memory, disk read and write rates, network in and out rates, and number of processes (PIDs). Click a row to open that container's [Statistics](/help/statistics) tab.

## Per-core load

One bar per CPU core. A bar turns amber above 60% and red above 85%. A single red bar while the others are idle usually means one process that cannot spread its work over several cores.

## Docker host

Facts about the engine DocMan manages: Docker version and API version, host name, operating system and architecture, kernel, storage driver, cgroup version, the socket DocMan talks to, DocMan's own version, and how long the host has been up along with its load averages.

## Storage

How much disk Docker's image layers take up, how many images and volumes there are, and their sizes, with shortcuts to [Images](/help/images) and [Volumes](/help/volumes).

> **Note:** If the host figures read "unavailable", DocMan cannot see the host's `/proc`. That happens when the Docker engine is remote, or not running on Linux, for example Docker Desktop on some set-ups. Per-container statistics still work.
