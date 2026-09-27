# Setting up DocMan

The first time DocMan starts it has no account. This page takes you from a freshly started container to a secured administrator account. If DocMan is not installed yet, see [Install, upgrade, back up](/help/administration) first.

## Before you begin

Decide on the name you will use to reach DocMan, such as `docman.example.lan`, and make sure it resolves to the host. Passkeys, the most secure way to sign in, are tied to that name and do not work with a bare IP address. The name should also be in `DOCMAN_HOSTNAMES` so it is included in DocMan's certificate. See [Passkeys need a hostname](/help/sign-in#passkeys-need-a-hostname).

## Step 1: find the setup key

On its first start DocMan prints a one-time setup key to its container log. On the host, run:

```bash
docker logs docman
```

Look for a block like this:

```
==============================================================
  DocMan first-run setup
==============================================================
  Open   https://<this-host>:9444
  Key    QK7MP-2XRTD-9WBHF-LNZ4S
```

- The key works once.
- A new key is generated every time DocMan restarts, until setup is finished.
- Lost the key? Run `docker restart docman`, then `docker logs docman` again.
- For automated provisioning you can fix the key in advance with the `DOCMAN_SETUP_KEY` environment variable.

## Step 2: open DocMan and enter the key

1. Browse to `https://<your-host>:9444`.
2. On first run the certificate is self-signed, so your browser warns you. Accept the warning once, or install your own certificate (see [Install, upgrade, back up](/help/administration#using-your-own-certificate)).
3. Enter the setup key and choose **Continue**. Letter case and dashes do not matter.

## Step 3: create the administrator

1. Choose a **Username**: 3 to 32 characters, using letters, digits, dot, dash and underscore.
2. Choose a **Password** of at least 12 characters that mixes letters with digits or symbols. The meter underneath shows its strength.
3. Enter it again in **Confirm password**, then choose **Create administrator**.

You are signed in and taken to **Settings → Passkeys**.

> **Note:** This first account can add more. Give everyone who manages the host their own account in **Settings → Users** rather than sharing a password, so each person signs in their own way and the activity log shows who did what. For scripts, use an [API token](/help/settings#api-tokens) instead. See [Users](/help/settings#users).

## Step 4: secure sign-in

A password on its own is the weakest way in. Choose at least one of these:

- **Register two passkeys** in **Settings → Passkeys**, for example this computer and your phone, or a hardware security key. When the second one is registered, password sign-in switches itself off. With two, losing one device never locks you out.
- **Enrol an authenticator app** in **Settings → Account → Authenticator app**. Password sign-in then also asks for a six-digit code, and it stays available even after you register two passkeys. This gives you a way in from any device.

Doing both is the most resilient setup: passkeys for everyday use, and password plus code as the fallback. [Signing in](/help/sign-in) explains how the methods interact.

## Step 5: look around

- The [Overview](/help/overview) shows what the host is doing right now.
- [Containers](/help/containers) lists everything that is already running on the host. DocMan picks up existing containers automatically.
- When you are ready, [deploy a container](/help/deploy).
