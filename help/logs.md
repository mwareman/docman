# Logs

The **Logs** tab shows everything the container writes to its standard output and standard error, live, as it happens. It works for running and stopped containers alike, so you can read why a container exited.

## Reading the log

- New lines appear at the bottom as the container writes them, and the view follows them.
- Lines the container wrote to standard error are shown in a different colour from standard output.
- Colours and bold text the application prints, using ANSI escape codes, are shown as colours. Other terminal control codes are dropped.
- The view holds the most recent 5,000 lines; older ones scroll away.

## The toolbar

| Control | What it does |
| --- | --- |
| **last 500** (the history menu) | How much earlier output to load when the view opens: the last 100, 500 or 2,000 lines, or **all lines**. Changing it reloads the log. The starting value comes from **Settings → Preferences → Log lines to load**. |
| **Timestamps** | Prefix every line with the time Docker received it. Changing it reloads the log. |
| **Wrap** | Wrap long lines to the width of the window. Untick it to keep each line on one row and scroll sideways instead. |
| **Filter lines** | Show only lines containing the text you type. The match ignores letter case. Clear the box to see everything again. |
| **Following** | Keeps the newest line in view. Scrolling up to read something pauses following automatically; choose **Following** again to jump back to the end. |
| **Clear** | Empties the view. It does not delete anything from Docker; reopen the tab to load the history again. |
| Download button | Saves the lines currently in the view to a text file named after the container. |

The status at the end of the toolbar reads **live** while connected. If the connection drops, for example because DocMan restarted, it reads **reconnecting…** and resumes by itself.

## Tips

- A container that stops straight after starting almost always explains why in its last few lines. Open **Logs** on the stopped container.
- To capture a problem as it happens, filter for a word like `error` and leave the tab open while you reproduce it.
- For a long-running investigation, turn on **Timestamps** so you can match events to the times on the [Statistics](/help/statistics) graphs.

> **Note:** DocMan reads the logs Docker keeps; it does not store any itself. How much history exists depends on the container's log driver and its size limits, not on DocMan.
