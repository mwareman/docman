# Volumes

A volume is storage that Docker manages and that outlives any single container. Databases, uploads and anything else a container must keep belong in a volume. Anything written elsewhere in a container is lost when the container is recreated.

**Volumes** lists every volume on the host and which containers use it.

## Reading the list

| Column | What it shows |
| --- | --- |
| **Volume** | Its name, with its location on the host underneath. |
| **Driver** | Usually `local`, meaning a directory on the host. Other drivers store data elsewhere, for example on network storage. |
| **Size** | The space it uses, when Docker reports it. |
| **Attached to** | The containers that mount it, as links. Hover over one to see where it is mounted and whether read-only. A volume nothing mounts is marked **unused**. |

Type in the **Filter** box to match names and host paths. Tick **Unused only** to see volumes no container mounts.

The ⓘ button shows a volume's details: driver, host path, scope, creation time, size, every container that mounts it with the path and state, and any driver options. The copy button copies its host path.

## Exploring a volume

**Explore**, on the right of each volume's row, opens the volume explorer: a file manager for what is inside the volume.

- **Folders** open with a click. The path bar at the top shows where you are; click any part of it to go back up, or use the up arrow.
- **Filter this folder** narrows the list as you type.
- Each item shows its size, when it was last changed, and its permissions and owner (user and group IDs), as `ls -l` would. Symbolic links show where they point.

What you can do:

| Action | How |
| --- | --- |
| **Upload files** | **Upload**, or drag files from your computer onto the list. Each shows its progress and a **Cancel** button. A file whose name is taken asks before replacing it. Large files are sent in pieces that pass any proxy's size limit, as for [image uploads](/help/images#large-archives-and-proxies). |
| **Download a file** | The download button on its row. |
| **Download a folder** | The download button on a folder's row, or **Download folder** for the one you are in. It arrives as a `.tar` archive. |
| **Edit a text file** | The edit button, or double-click the file. See below. |
| **Create a file or folder** | **New file** or **New folder**. A new file opens in the editor straight away. |
| **Rename or move** | The tag button. Change the name, or the folders in the path to move it elsewhere in the volume. |
| **Delete** | The bin button, after confirmation. A folder is deleted with everything in it. |

New files and folders take the owner of the folder they are created in, so an application that runs as a non-root user can still use them. Editing a file keeps its owner and permissions.

### Editing a file

The editor opens text files of up to 1 MB. Larger files, and files that are not text, are downloaded instead.

- **Save**, or Ctrl+S (⌘S on a Mac), writes the file back.
- Closing with unsaved changes, including with Escape, asks before throwing them away.
- If the file changed on disk since you opened it, for example because the application rewrote it, saving is refused rather than overwriting that change. Close and reopen the file to see the current version.

> **Warning:** Changes take effect in the volume immediately, including for containers that are running. An application may not notice until it is restarted, or may overwrite your change. For configuration files, change them and then restart the container.

When you explore DocMan's own data volume, a warning says so: it holds DocMan's database and TLS keys, and changing or deleting files there can break DocMan.

### How it works

DocMan starts a small helper container for the volume, from DocMan's own image, with the volume mounted and no network. It runs only while the explorer is open: leaving the explorer, or closing the browser tab, removes it at once, and one left behind by a lost connection is removed after three minutes without use. It is also removed when you delete or prune the volume, and it leaves nothing behind. Helpers do not appear in the container list, and do not count as a container using the volume. Every change made through the explorer is recorded in **Settings → Activity**.

### Editing JSON

Files ending in `.json`, and JSON files with no extension, get extra help in the editor:

- A file written as one long line, as many applications do, opens **laid out** over several lines so it is readable and easy to change. The line under the file name says so.
- **Save on one line, as it was** is ticked for such a file, so saving puts it back on one line in the file, the way the application wrote it. Untick it to keep the laid-out version.
- The footer says **Valid JSON**, or what is wrong and where. **Format** lays out the JSON at any time.
- Saving JSON that is not valid asks first, since the application reading it will probably fail.

Laying out and compacting only moves spaces and line breaks: every value is kept exactly, including very large numbers.

## Creating a volume

The deploy wizard creates the volumes a new container needs by itself, so you rarely need to create one by hand. To make one in advance, for example to fill it before a container uses it:

1. Choose **New volume**.
2. Enter a **Name**: letters, digits, dot, dash and underscore.
3. Optionally open **Driver and options**. **Driver** lists the volume drivers installed on this host: always `local`, a directory on the host, plus any volume plugins that are enabled. **Options** are passed to the driver, one `key=value` per line. For example, the `local` driver can mount network storage with `type=nfs`, `o=addr=10.0.0.5,rw` and `device=:/export/data`.
4. Choose **Create volume**.

Then mount it in a container from the Storage section of its [configuration](/help/configuration#storage). Volume names are suggested as you type.

## Deleting a volume

The bin button deletes a volume and everything in it, after confirmation. Docker refuses while any container, running or stopped, still mounts it. Remove those containers, or change their storage, first.

## Pruning

**Prune unused** deletes every volume that no container mounts, and reports how many it deleted and how much space it freed.

### Automatic clean-up

At startup and once a day, DocMan removes **anonymous** volumes (the ones with a long random name) that no container uses and that are **completely empty**. Earlier DocMan versions left such volumes behind when exploring a volume or recreating a container. A volume with anything in it, or that any container still uses, is never touched. Each clean-up is recorded in **Settings → Activity**.

> **Warning:** Deleting or pruning a volume deletes its data permanently. A stopped container you plan to start again still counts as using its volumes, but a container you have removed does not. Check **Unused only** before pruning.

## Backing up a volume

DocMan does not back volumes up. From the host, a throwaway container can archive one:

```bash
docker run --rm -v myapp-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/myapp-data.tar.gz -C /data .
```

Restore into a new volume the same way with `tar xzf`. Stop or [pause](/help/container#starting-stopping-and-pausing) the container first if the application writes to the volume constantly, such as a database.
