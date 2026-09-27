# Deploying a container

The **New container** wizard creates a container from an image. It reads the image first and fills in what the container needs, so for most images you only need to check the suggestions and choose **Save and start**.

Open it with **New container** on the Containers or Images page, or with **Deploy** on an image's row in Images to skip straight to step 2 with that image.

## Step 1: choose the image

The wizard offers three sources:

| Source | Use it when |
| --- | --- |
| **From an image on this host** | The image is already on the host. Pick it from the list; type to filter. |
| **Pull from a registry** | The image is on Docker Hub, GitHub, a private registry or any other. Choose the registry, find the image and a tag, and choose **Pull image**. See [Pulling from a registry](#pulling-from-a-registry). |
| **Upload an image archive** | The host cannot reach a registry. Choose a file made with `docker save`, as `.tar`, `.tar.gz` or `.tgz`, and choose **Import image**. |

Progress for a pull or an upload is shown as it happens. When it finishes, the wizard moves on by itself.

> **Tip:** To load a new version of an image without creating a container, use **Images → Upload image** instead. See [Updating containers](/help/updating).

### Pulling from a registry

1. **Registry** lists the registries set up in [Settings → Registries](/help/settings#registries). The default one is selected first; Docker Hub is the default until you choose another. Under it you see the registry's address and whether DocMan signs in to it. **Add** sets up a new registry without leaving the wizard.
2. **Image**: type the image's name. For registries that can be searched, matches appear as you type, with their description (and, on Docker Hub, stars and an **official** badge); choose one. Otherwise type the full path, such as `group/project/image` on GitLab.
3. **Tag**: once the image is known, its tags are listed, newest versions first, with `latest` at the top. Pick one or type your own. Left empty, `latest` is used.
4. The line below shows the full reference that will be pulled, such as `ghcr.io/home-assistant/home-assistant:stable`.
5. Choose **Pull image**.

The registry's saved sign-in is used for the pull. To pull once with other credentials, open **Use other credentials for this pull**; those are used for this pull only and are not stored.

For a **GitHub repository** registry, the wizard lists the repository's pre-built images instead, one card per image name, with the newest version for this host selected. Pick another version from the list if you need one (versions that do not run on this host are shown but cannot be chosen) and choose **Install**. DocMan downloads the archive, loads it and moves on to step 2 with its moving tag, such as `latest`, so the container follows later versions. See [GitHub repositories](/help/settings#github-repositories).

If you type a name that starts with an address, such as `quay.io/prometheus/busybox`, that address is used rather than the registry selected above, and the preview says so. The sign-in saved for that address, if there is one, is still used.

## Step 2: review the plan

DocMan inspects the image and proposes a complete configuration. The chips at the top show the image, its platform, its size and a summary of what needs your attention.

What DocMan works out for you:

- **A name**, taken from the image and made unique on this host, such as `nginx` or `nginx-2`.
- **Environment variables** the image declares, with their defaults. Variables the image leaves empty are marked **required**, because the image probably will not work without them. Variables whose names look secret, containing `PASSWORD`, `SECRET`, `TOKEN`, `API_KEY` or `CREDENTIAL` for example, are hidden as you type, and flagged if the image ships a default value you should replace. Housekeeping variables every image inherits, such as `PATH` and `HOME`, are left out of the form; the container still gets the image's values for them.
- **Storage** for every path the image declares as a volume, as a named volume such as `postgres-data`. Without one, that data would be lost whenever the container is recreated.
- **Published ports** for every port the image exposes. DocMan uses the same port number on the host where it is free. Where it is already taken it picks the next free one and says so. Low port numbers (below 1024) that are taken move up by 8000, so 80 becomes 8080.
- **Restart policy** set to **Unless stopped**, so the container comes back after a crash or a reboot but stays down if you stop it.

Warnings above the form point out things to check. For example, the image declares no command and so would exit immediately, or it runs as root.

Everything in the form can be changed before you create the container. The fields are the same as on a container's Configuration tab and are described in [Changing configuration](/help/configuration#the-configuration-form).

## Step 3: optionally fix the address

Tick **Give this container a fixed internal address** to put it on DocMan's own network at an address that never changes. Leave **Address** empty for the next free one, or enter a specific address. See [Fixed addresses](/help/addresses).

## Step 4: create it

- **Save and start** creates the container and starts it.
- **Save without starting** creates it stopped. Start it later from the container list.

DocMan then opens the new container's page. If the container was created but would not start, for example because a required variable is missing or a port is already in use, you are told why. The container still exists, so fix its configuration and start it again.

> **Note:** Named volumes in the plan are created automatically when the container is created. If a volume with that name already exists, the container uses it, along with the data already in it.

## Checking the result

On the new container's page:

- **Overview** shows its state, address and published ports. Click a port to open it in the browser.
- **Logs** shows what it printed while starting. This is the first place to look if it stopped straight away.
