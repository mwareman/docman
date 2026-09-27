# Statistics and processes

The **Statistics** tab shows a running container's resource use live, with a short history for each figure. It refreshes every two seconds by default; change the rate in **Settings → Preferences → Statistics interval**.

## The four panels

| Panel | Headline figure | Details |
| --- | --- | --- |
| **CPU** | Percentage of one core. A container using two full cores shows 200%. | How many cores' worth it is using, out of how many it can use, and its number of processes. |
| **Memory** | Memory in use. | When a memory limit is set: the share of the limit, as a bar that turns amber above 75% and red above 90%. Otherwise **no limit set**. |
| **Disk I/O** | Combined read and write rate. | Read and write rates separately, and the totals since the container started. |
| **Network** | Combined traffic rate. | Incoming and outgoing rates, and the totals since the container started. |

Memory is counted the way `docker stats` counts it: page cache the kernel can reclaim is left out, so the figure is what the container really holds.

The **live** marker in the CPU panel shows the figures are current. If the connection drops it reads **reconnecting…** and resumes by itself.

> **Tip:** A memory figure that climbs steadily towards the limit and then drops back to almost nothing, with the container restarting, usually means it is being killed for running out of memory. Its [exit code](/help/container#the-container-card) is then `137`. Raise the limit in [Changing configuration](/help/configuration#limits), or find out why the application grows.

## Processes

The **Processes** table lists what is running inside the container, as `ps` would show it: user, process ID, CPU and memory use, start time and command. It is read once when the tab opens; choose **Refresh** to read it again.

It is empty for a stopped container.

## Comparing containers

To see which container is using the most of the host, use the table on the [Overview](/help/overview#container-share-of-the-host) page instead. It shows every running container side by side, busiest first.
