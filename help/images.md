# Images

An image is the read-only template a container is created from. **Images** lists every image on the host and lets you get new ones, look inside them, tag them and remove them.

## Reading the list

| Column | What it shows |
| --- | --- |
| **Image** | The repository name, such as `nginx`, with the start of the image ID underneath. An image with no name at all is shown as **untagged image**. |
| **Tags** | Every tag pointing at this image, such as `1.27` and `latest`. An image with none is marked **dangling**: usually an older version replaced by a newer one under the same tag. |
| **Version** | Whether a newer version is waiting in the image's registry. See [Checking for newer versions](#checking-for-newer-versions). |
| **Size** | The space its layers take up. Images often share layers, so these sizes add up to more than the disk actually used. |
| **Created** | When the image was built, not when it arrived on this host. |
| **In use** | How many containers, running or not, were created from this exact image. Hover to see their names. On an untagged image the count is amber: those containers are still running an old version, and recreating them moves them to the current one. |

Type in the **Filter** box to match names, tags or IDs. Tick **Untagged only** to see the dangling images, the usual candidates for cleaning up.

## Getting an image

- **New container** opens the [deploy wizard](/help/deploy), which can pull or upload an image and then create a container from it.
- **Upload image** imports an archive without creating a container. See below.
- **Deploy** on an image's row opens the deploy wizard with that image already chosen.

### Uploading an image archive

**Upload image** imports a file made with `docker save`, as `.tar`, `.tar.gz` or `.tgz`. Drag the file onto the dialog or click to choose it, then choose **Upload and import**. It happens in two stages, each with its own progress bar:

1. **Uploading**: the file goes to DocMan, with the share done, the amount sent, the speed and the time left.
2. **Importing into Docker**: Docker reads the archive on the server.

While it is under way the dialog stays open: **Close**, the ✕ and clicking outside are unavailable, and leaving the page asks first. **Cancel upload** stops it during the upload stage and imports nothing. Once the upload stage is complete there is nothing left to cancel, and the button goes. When the import is done, **Upload complete** says which images arrived, and **Close** returns.

#### Large archives and proxies

Proxies and firewalls in front of DocMan, such as Cloudflare, cloudflared, nginx, lighttpd and Traefik, limit how large one request may be. A large archive sent in one go would be cut off. So DocMan sends it in pieces, each its own request:

- The piece size is found automatically. If a piece is refused or cut off, it is sent again, smaller. While pieces go through quickly they grow. You never need to know or set the limit.
- Only whole pieces that arrived completely are kept, and the import starts only once every byte is there. A partial or damaged archive is never imported.
- The import itself runs on the server, so a long import cannot hit a proxy's time limit.

The archive is held in DocMan's data volume until it has been imported, so that volume needs room for it. Unfinished uploads are discarded after a day, and whenever DocMan restarts.

When it finishes, the dialog lists the tags the archive contained. If a tag already existed, it now points at the uploaded version, and containers using that tag switch to it when you [recreate them](/help/updating#updating-an-image-that-is-not-in-a-registry).

> **Tip:** An archive made from an image ID rather than a tag (`docker save 3f9c...`) arrives untagged. Save by tag, such as `docker save myapp:latest`, or add a tag after uploading.

## Checking for newer versions

For every image that was pulled from a registry, DocMan asks the registry once a day whether the tag now points at a newer version. It downloads nothing: it compares the registry's current version of the tag with the one on this host. The **Version** column shows the result:

| Version | Meaning |
| --- | --- |
| **Up to date** | This is the newest version of the tag in its registry. |
| **Newer version available** | The registry has a newer version. Choose **Pull** to download it. |
| **uploaded** | Imported from an archive. It is not checked, because such images are usually in no registry. |

Images installed from a [GitHub repository](/help/settings#github-repositories) are checked against that repository instead: **Newer version available** means it has a newer archive of the image, and **Pull** installs it. Hover over **Up to date** to see the version installed, or that the tag is pinned to its version.
| **local** | Built or loaded on this host, or in no registry DocMan can reach, such as a private registry whose sign-in is not set up in [Settings → Registries](/help/settings#registries). |
| **can't check** | The registry could not be reached this time. Hover for the reason; it is tried again next time. |
| **not checked yet** | The image arrived since the last check. |

- **Refresh**, at the top of the page, runs the check straight away for every image. The text beside it says when the last check ran; hover for when the next automatic one is due.
- The daily check keeps its schedule across restarts: it runs a day after the last one.

> **Note:** Checking private registries needs credentials, which DocMan does not store. Such images show as **local**. Pull them with credentials from the deploy wizard instead.

### Pulling the newest version

**Pull** appears on every image that came from a registry. It downloads the newest version of the image's tag, with live progress, and then checks it again. If there was nothing newer, it says so.

Pulling changes only the image. Containers keep running the version they were created with, and show **Update available** in the container list until you recreate them. See [Updating containers](/help/updating).

## Looking inside an image

The ⓘ button opens the image's details, in four tabs:

| Tab | What it shows |
| --- | --- |
| **Overview** | Its ID (with a copy button), platform, size, when it was built, its default entrypoint, command, user and working directory, whether it has a health check, and its labels. |
| **Environment** | Every environment variable the image sets, with its default value. |
| **Ports and volumes** | The ports it exposes and the paths it declares as volumes. The deploy wizard publishes and creates these for you. |
| **Layers** | How the image was built: one row per build step, newest first, with the size it added and the instruction that produced it. Steps that add no data, such as setting a label, are hidden until you untick **Hide steps that add no data**. |

## Adding a tag

The tag button adds another name to an image, such as `myapp:stable` alongside `myapp:1.4`. It does not copy anything: both names point at the same image. Use it to keep a known-good version under a name that will not move, or to name an image that arrived untagged.

## Removing an image

The bin button removes an image, after confirmation.

- If the image has several tags, removing it takes away one tag, and the image stays until its last tag is gone.
- Docker refuses to remove an image a container still uses. Tick **Force, even if containers still reference it** to untag it anyway; the containers keep running on the image layers, which are removed once no container needs them.

## Pruning

**Prune** removes every dangling image that no container uses, and reports how much space it reclaimed. Tick **Also remove tagged images that no container uses** to clear out everything not in use, including images you pulled but never ran.

> **Warning:** Pruned images are gone from the host. Anything you pulled can be pulled again, but an image that only ever existed here, such as one you uploaded, has to be uploaded again.
