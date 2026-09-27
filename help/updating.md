# Updating containers

A container runs one exact version of its image, fixed when the container was created. Pulling or uploading a newer version of the image does not change a container that already exists. To move a container to the new version, **recreate** it.

## How tags make this work

An image reference such as `myapp:latest` or `nginx:1.27` is a **tag**: a name that points at one exact image version. When a new version is pulled or uploaded under the same tag, the tag moves to it, and the old version stays on the host, untagged, for as long as a container still uses it.

A container remembers the tag it was created from. **Recreate** builds a new container from that tag, and so from whatever version the tag points at now.

DocMan watches for this. When a newer image is on the host under a container's tag, the container shows **Update available** in the container list and on its own page. That covers images uploaded through DocMan, loaded with `docker load` on the host, and pulled.

## Updating from a registry

This also covers images installed from a [GitHub repository](/help/settings#github-repositories): for those, "pulling" means downloading and loading the repository's newest archive of the image.

For images that come from Docker Hub or another registry, DocMan checks the registry once a day. When it finds a newer version, the container shows **Update available** in the container list, and the image shows **Newer version available** in [Images](/help/images#checking-for-newer-versions). To check now instead of waiting, choose **Images → Refresh**.

To update the container:

1. Choose **Recreate** on its row in the container list, or on its Overview tab.
2. **Pull the newest version of the image first** is already ticked for a registry image. Leave it.
3. Choose **Recreate**.

DocMan pulls the tag, then rebuilds the container from it. If the registry had nothing newer, the container is simply rebuilt from the same version.

To download a newer version without touching any container yet, choose **Pull** on the image in **Images**. Containers then show **Update available** until you recreate them.

## Updating an image that is not in a registry

For images you build yourself and deliver as a `docker save` archive:

1. Build and save the new version under **the same tag** the container uses. For example, if the container runs `myapp:latest`:

```bash
docker build -t myapp:latest .
docker save myapp:latest | gzip > myapp.tar.gz
```

2. In DocMan, go to **Images → Upload image**, choose the file and choose **Import image**. Only the image is imported; no container is created.
3. The container now shows **Update available** in the container list. Open it and choose **Recreate**. DocMan remembers that this tag was uploaded, so **Pull the newest version of the image first** starts unticked, and a note says the version on this host will be used.
4. Choose **Recreate**. The container is rebuilt from the version you just uploaded.

> **Note:** DocMan leaves the pull unticked only for tags uploaded through DocMan itself. For an image loaded with `docker load` on the host, untick it yourself; a pull of an image that is in no registry fails.

A successful pull of the same tag later clears the note, and DocMan goes back to offering a pull.

## What survives a recreate

| Kept | Lost |
| --- | --- |
| The container's name and configuration | The container's own filesystem, outside volumes |
| Named volumes and their data | Running processes and anything held in memory |
| Anonymous volumes and their data: the new container reuses them | |
| Host-path mounts | The container ID, which changes |
| The fixed address, if it has one | |
| Every network it is attached to | |

If the new container cannot be created, DocMan puts the original back and restarts it. If it is created but will not start, the original is already gone. The new container's page opens with the error so you can fix its [configuration](/help/configuration) or recreate it with a working image.

## Cleaning up old versions

The previous version of an image stays on the host, untagged and marked **dangling** in [Images](/help/images), until you remove it. Choose **Images → Prune** to remove every dangling image that no container uses.

## Updating DocMan itself

DocMan can recreate itself: use **Recreate** on its own container, which carries the **DocMan** badge. A helper container does the swap, and the page reloads when the new DocMan answers. See [DocMan's own container](/help/container#docman-s-own-container).

To install a new DocMan image first:

- From a registry: **Recreate** with **Pull the newest version** ticked.
- From an archive: **Images → Upload image** with the new DocMan archive, then **Recreate** DocMan.

> **Warning:** DocMan versions before 1.1 cannot recreate themselves safely: the old container stops and the new one is left not running, which locks you out. Upgrade such an installation from the host instead. See [Install, upgrade, back up](/help/administration#upgrading).
