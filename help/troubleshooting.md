# Troubleshooting

Common problems, what causes them, and how to fix them.

## A container stops straight after starting

1. Open the container and look at its **Exit code** on the Overview tab.
2. Open **Logs**. The last lines almost always say what went wrong.

| Exit code | Usual cause |
| --- | --- |
| `0` | The program finished. It is a one-off job, or it has no long-running command. Check the image's documentation for how to run it as a service. |
| `1` or another low number | The application failed, for example a missing setting, a bad configuration file or a database it cannot reach. The log says which. |
| `126` or `127` | The command could not be run or was not found. Check **Entrypoint** and **Command** in [Configuration](/help/configuration#advanced-options). |
| `137` | Killed: stopped with **Kill**, or by the kernel for running out of memory. Raise the memory limit or find out why it grows. |
| `139` | The program crashed (segmentation fault). Often the wrong image architecture for the host. |

If the deploy wizard warned that a variable was **required**, check that it has a value.

## "port is already allocated" or "address already in use"

Another container, or a program on the host, already uses that host port. Change the **Host** port in the container's [Published ports](/help/configuration#published-ports), or stop whatever holds it. The container list shows which containers publish which ports.

## A container keeps restarting

Its restart policy brings it back each time it crashes, and its **Restarts** count keeps rising. Read its **Logs** to see why it crashes. To stop the loop while you investigate, choose **Stop**: with the **Unless stopped** policy it then stays down.

## I cannot reach a container's port from another computer

- Check that the port is published: it appears in the container's **Published** list. Ports the image exposes are not reachable from outside until they are published.
- Check the **Bind address**. `127.0.0.1` means the host only.
- Check the host's firewall allows the port.
- Make sure the application inside listens on all interfaces (`0.0.0.0`), not only on `localhost` inside the container.

## Containers cannot reach each other by name

Name lookup between containers only works on a user-defined network, not on Docker's default `bridge`. Put both containers on the same user-defined network, for example by giving them both a [fixed address](/help/addresses), which places them on `docman0`, where each answers to its container name.

## An address shows as "drifted"

The container is not at its reserved address, usually because it was recreated outside DocMan or Docker reassigned addresses after a restart. Choose **Re-apply** on its row on the Addresses page, or **Re-apply addresses** for all of them. DocMan also repairs drift every time it starts.

## Recreate fails with a pull error

The image is not in any registry the host can reach, typically an image you uploaded, or the host has no internet access. Recreate again with **Pull the newest version of the image first** unticked. See [Updating containers](/help/updating#updating-an-image-that-is-not-in-a-registry).

## Recreate did not pick up my new image

The container is rebuilt from its tag. Check that the new image was saved and uploaded under exactly the tag the container uses: the **Image** field on its Overview tab, such as `myapp:latest`. In **Images**, the new version should carry that tag, and the old one should show as **dangling**.

## The console will not open

- The container must be running.
- The image must contain `/bin/sh`. Minimal and distroless images do not; see [The console](/help/console#when-it-will-not-connect).
- The **User** must exist in the image. Leave it empty to use the default.

## Logs or statistics say "reconnecting…"

The live connection to DocMan dropped, usually because DocMan restarted or the network blinked. It reconnects by itself. If it never does, check that DocMan is running and, behind a reverse proxy, that the proxy passes WebSocket connections through.

## Registry search or tags fail with a certificate error

Searching most registries and listing tags are done by DocMan itself, not by Docker. If the host reaches the internet through a proxy that inspects TLS (common on corporate networks), DocMan does not trust that proxy's certificate and reports `certificate signed by unknown authority`. Pulls still work, because Docker has its own trust settings. Mount the proxy's root certificate into DocMan's container and point DocMan at it:

```bash
-v /etc/docman-ca:/extra-ca:ro -e SSL_CERT_DIR=/extra-ca
```

## A registry refuses: "the image may not exist, or it is private"

Registries such as Docker Hub answer the same way for an image that does not exist and for a private one you may not see. Check the spelling of the path, then check that the registry in [Settings → Registries](/help/settings#registries) has a sign-in that can read the image, and use **Test** on its row.

## A large upload fails behind a proxy

DocMan sends uploads in pieces and shrinks them automatically when a proxy refuses or drops one, down to 64 KB. If an upload still fails with "keeps failing even in the smallest pieces", the problem is the connection rather than a size limit: check that DocMan is reachable, and that the proxy allows `POST` requests to `/api/uploads/`.

## Fixed addresses no longer show as fixed

This happens when DocMan's database was replaced, for example by moving DocMan to a new data volume. Restart DocMan, or choose **Addresses → Re-apply addresses**: containers still on `docman0` with a fixed address are recognised and recorded again. See [If DocMan loses its records](/help/addresses#if-docman-loses-its-records).

## Host figures on the Overview are unavailable

DocMan reads host CPU, memory and disk figures from the host's `/proc`. They are unavailable when Docker is remote or not running on Linux. Per-container statistics still work.

## I cannot create a passkey

- Browse to DocMan by its **host name** over **HTTPS**, never by IP address. See [Passkeys need a hostname](/help/sign-in#passkeys-need-a-hostname).
- Use a current version of Chrome, Edge, Safari or Firefox.
- If DocMan is behind a reverse proxy, set `DOCMAN_RP_ID` to the public host name.

## Passkeys stopped working

The host name they are bound to changed: you are browsing to a different name, or **Relying party ID** or `DOCMAN_RP_ID` was changed. Browse to the original name, or sign in another way, and register new passkeys for the new name.

## I am locked out

See [If you are locked out](/help/sign-in#if-you-are-locked-out).

## DocMan says it cannot reach Docker

DocMan cannot open the Docker socket.

- Check the container has `-v /var/run/docker.sock:/var/run/docker.sock`.
- On SELinux hosts, add `--security-opt label=disable`.
- Check Docker itself is running: `sudo systemctl status docker`.

## Where to look next

- DocMan's own log, from the host: `docker logs docman`.
- **Settings → Activity** shows every change DocMan made, including failures and their error messages.
- The [API reference](/help/api) documents exactly what each action does.
