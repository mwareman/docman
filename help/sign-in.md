# Signing in

Every DocMan user has an account of their own, with its own password, passkeys and authenticator app. An account has up to three ways to sign in. This page explains each one, how they switch each other on and off, and how to keep a way in if you lose a device.

Everything on this page applies to each account separately. One person registering passkeys or enrolling an authenticator app changes nothing for anyone else. Accounts are added and removed in [Settings → Users](/help/settings#users).

## The three ways in

| Method | Available for an account when |
| --- | --- |
| **Passkey** | Always, once at least one is registered. |
| **Password** | While fewer than two passkeys are registered. |
| **Password and authenticator code** | Whenever an authenticator app is enrolled, even with two or more passkeys. |

In practice, for your own account:

- Registering a **second passkey** switches plain password sign-in off. From then on only passkeys work, unless an authenticator app is enrolled.
- Enrolling an **authenticator app** makes password sign-in two-step, password plus a six-digit code, and keeps it available permanently as the alternative to passkeys.
- Removing passkeys so that fewer than two remain turns password sign-in back on.

## Passkeys

A passkey is a sign-in credential held by your device, your password manager or a hardware security key. It cannot be phished or reused on another site, and there is nothing to type.

- Add passkeys in **Settings → Passkeys** with **Add a passkey**. Your browser asks where to keep it: this device, a phone, a password manager or a security key.
- Register **two**, on different devices, so that losing one never locks you out.
- On the sign-in page choose **Sign in with a passkey**.
- Rename or remove passkeys from the same list. The list shows when each was added and last used, and marks passkeys your password manager syncs between devices as **synced**.

### Passkeys need a hostname

Browsers only allow passkeys on a secure connection and for a proper host name, never a bare IP address. And a passkey only works for the name it was created on.

- Always reach DocMan by the same name, such as `https://docman.example.lan:9444`, not `https://192.168.1.20:9444`.
- Put that name in DNS or in the hosts file of the computers you manage DocMan from, and list it in `DOCMAN_HOSTNAMES` so it is in DocMan's certificate.
- Behind a reverse proxy, set `DOCMAN_TRUST_PROXY=true` and `DOCMAN_RP_ID` to the public host name.

If the sign-in page says passkey sign-in is unavailable, the address in your browser cannot be used for passkeys: it is an IP address, it is not the name passkeys are bound to, or the connection is not secure. The message underneath gives the reason.

> **Warning:** Changing the host name passkeys are bound to, in **Settings → Preferences → Relying party ID** or with `DOCMAN_RP_ID`, makes every existing passkey stop working. Make sure another way in is available first.

## Authenticator app

An authenticator app generates a new six-digit code every 30 seconds. Any app that supports standard TOTP codes works: Google Authenticator, Microsoft Authenticator, 1Password, Bitwarden, Aegis, Authy and many more.

To enrol one:

1. Go to **Settings → Account** and choose **Set up authenticator app**.
2. Scan the QR code with the app. On a phone you can use **Open in an installed app** instead, or type the secret shown under the code.
3. Enter the six-digit code the app now shows, and choose **Enable**.

The secret is only saved once a working code proves the app is set up, so abandoning the dialog changes nothing.

From then on the sign-in page asks for **Authenticator code** along with the password. Each code works once.

To remove it, choose **Remove authenticator** in **Settings → Account** and confirm with your password. If two passkeys are registered, password sign-in then switches off.

## Passwords

- At least 12 characters, mixing letters with digits or symbols.
- Change it in **Settings → Account → Password**; you need the current one.
- Repeated failed sign-ins from one address are slowed down, so guessing is impractical.

## Sessions

Signing in in a browser starts a session that lasts 12 hours by default (set `DOCMAN_SESSION_TTL` to change it). After that you are asked to sign in again.

- **Settings → Account → Active sessions** lists every signed-in browser, with how it signed in, its address, its browser and when it was last seen.
- **Sign out other sessions** ends every session except the one you are using. Do this if you signed in on a computer you no longer trust.
- Sign out of your own session with the button beside your name at the bottom of the menu.

The sign-in page offers the password form while at least one account can use a password, and the passkey button once any passkey is registered. If your own account has password sign-in switched off, the page tells you so after you enter the right password, and you sign in with a passkey instead.

## If you are locked out

- **Lost one passkey:** sign in with the other, then remove the lost one in **Settings → Passkeys** and register a replacement.
- **Lost every passkey, but the authenticator app is enrolled:** sign in with your password and a code.
- **Lost every way in:** ask another DocMan user to choose **Reset sign-in** for your account in **Settings → Users**. You get a new password from them; your passkeys and authenticator app are removed, so register them again once you are in.
- **Every user is locked out:** anyone with access to the host can start DocMan afresh by deleting its database, which also removes every account, API token, fixed-address record, preference and the activity log. Containers, images and volumes are not affected. Stop DocMan, delete `docman.db` from its `/data` volume, and start it again. It then prints a new setup key, as on [first start](/help/getting-started).

> **Tip:** Having at least two users is the simplest insurance against a lockout: each can reset the other.
