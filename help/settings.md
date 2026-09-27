# Settings

**Settings** has seven tabs: **Account**, **Passkeys**, **Users**, **API tokens**, **Registries**, **Preferences** and **Activity**. Account and Passkeys are about your own account; the rest apply to all of DocMan.

## Account

- **Who you are**: how you are signed in, when the account was created, and how many passkeys it has. Change your **Username** here and choose **Save**. Usernames must be unique, ignoring letter case.
- **Password**: whether password sign-in is currently on, and why. Change the password with the current one, then the new one twice.
- **Authenticator app**: enrol or remove an app that adds a six-digit code to password sign-in. See [Signing in](/help/sign-in#authenticator-app).
- **Active sessions**: every browser signed in to DocMan, with **Sign out other sessions**. See [Sessions](/help/sign-in#sessions).

## Passkeys

Lists your passkeys and adds new ones. The bar at the top fills as you register them; at two, password sign-in switches off for your account unless you have an authenticator app. Each row shows the passkey's name, type, when it was added and last used, with buttons to rename or remove it. See [Passkeys](/help/sign-in#passkeys).

## Users

Everyone who manages the host should have their own account. Each account has its own password, passkeys and authenticator app, and everything a user does is recorded under their name in the activity log.

> **Note:** Every user is a full administrator. DocMan controls the host through the Docker socket, which is equivalent to root, so there are no restricted accounts. Only add people you would trust with root on the host.

The list shows each user, how they can currently sign in (**password**, **password + code** with an authenticator app, and their number of passkeys), when they were added and when they last signed in. Your own account is marked **you**.

### Adding a user

1. Choose **Add a user**.
2. Enter a **Username**: 3 to 32 characters, using letters, digits, dot, dash and underscore.
3. DocMan fills in a strong **Starting password**. Use **Generate** for another, or type your own of at least 12 characters mixing letters with digits or symbols. **Copy** it.
4. Choose **Add user**, then give the new user their username and password by a secure route.

They sign in with that password, change it in **Settings → Account**, and add their own passkeys or authenticator app.

### Resetting a user's sign-in

When someone is locked out, for example because they lost their passkeys, choose **Reset sign-in** on their row. Set a new starting password (one is generated for you) and confirm. DocMan then:

- gives their account the new password;
- removes their passkeys and their authenticator app;
- signs out every session they have.

Nothing else can sign in as them afterwards, and they set up passkeys or an authenticator app again once they are in. You cannot reset your own account this way; change your own sign-in in **Account** and **Passkeys**.

### Removing a user

The bin button removes a user, after confirmation. Their passkeys and sessions are deleted, and they are signed out at once. Their entries in the activity log stay, and API tokens they created keep working.

You cannot remove the account you are signed in with, or the last remaining account. To remove your own account, sign in as another user.

## API tokens

API tokens let scripts and other tools use DocMan's [REST API](/help/api) without a browser.

Tokens belong to DocMan, not to the person who created them:

- Every user sees every token, and any user can revoke one.
- A token keeps working if the user who created it is renamed or removed.
- A token cannot sign in to the UI, and has no password, passkeys or sessions of its own. Account settings are the one thing it cannot change.
- Everything done with a token is recorded in the activity log under **the token's name**, so name tokens after where they are used.

### Creating a token

1. Choose **New token**.
2. Give it a **Name** that says where it will be used, such as `ci-deploy`.
3. Optionally set **Expires in (days)**. Leave it empty or `0` for a token that never expires.
4. Choose **Create token**, then **Copy** the token straight away.

> **Warning:** The token is shown once only. DocMan stores just a fingerprint of it and cannot show it again. If you lose it, revoke it and create a new one.

A token has full rights over DocMan: it can do anything a user can, including managing users. Keep it as safe as a password, and give each script its own token so you can revoke one without affecting the others.

Send it in an `Authorization: Bearer` header:

```bash
curl -sk -H "Authorization: Bearer dm_ab12cd34.xxxxxxxx" https://docman.example.lan:9444/api/containers
```

### Managing tokens

| Column | Meaning |
| --- | --- |
| **Name** | What the token was called when it was created. |
| **Prefix** | The start of the token, such as `dm_ab12cd34…`, to recognise it by. |
| **Created by** | The user who created it, by their current name. **removed** means that user no longer exists; the token still works. A token created by another token names that token. **not recorded** means it was created before DocMan 1.5 kept track. |
| **Created** | When it was created. Hover for the exact time. |
| **Last used** | When it last made a request, or **never**. A token unused for a long time is a good candidate for revoking. |
| **Expires** | When it stops working, or **never**. |
| **State** | **active**, **expired** or **revoked**. |

- The ✕ button **revokes** a token. Anything using it stops working immediately.
- A revoked token stays in the list for reference. Its bin button deletes the record.

## Registries

The registries DocMan searches and pulls images from. You choose one when you [pull an image in the New container wizard](/help/deploy#pulling-from-a-registry), and its sign-in is also used whenever DocMan contacts that registry for you: **Images → Pull**, **Recreate** pulling a newer image, and the [daily update check](/help/images#checking-for-newer-versions). DocMan matches an image to a registry by the address at the start of its name; names without one, such as `nginx`, are Docker Hub.

**Docker Hub** is set up the first time you open the list, without a sign-in, and is the **default**: the registry the wizard selects first. **Make default** on another row changes that. The default cannot be removed; make another registry the default first.

Each row shows the registry's name (with its kind underneath when you renamed it), its address, who it signs in as, and when it was last changed. **Test** checks that the registry answers and, when a sign-in is set, that the registry accepts it.

### Adding a registry

1. Choose **Add a registry**.
2. Pick the **Kind**. The **Name** and, for public services, the **Address** are filled in for you. Change the name to anything you like, for example to tell two GitHub accounts apart, and change the address for a self-hosted install.
3. Choose how DocMan **signs in**. The methods offered depend on the kind, and the text underneath says which credentials to use.
4. Choose **Add registry**.

| Kind | Address | Sign-in | Search |
| --- | --- | --- | --- |
| **Docker Hub** | `docker.io` | None for public images, or your username and a personal access token. Signing in raises Docker Hub's pull limits. | Yes |
| **GitHub Container Registry** | `ghcr.io` | Your GitHub username and a personal access token (classic) with `read:packages`. None for public images. | The packages the token can see |
| **GitHub Enterprise** | your GitHub Enterprise container address, such as `containers.github.example.com` | Your username and a personal access token with `read:packages`. **API address** is optional; it defaults to `https://<your GitHub host>/api/v3`. | The packages the token can see |
| **GitLab Container Registry** | `registry.gitlab.com`, or your own GitLab's registry | A username with a personal access token (`read_registry`), or a deploy token's username and token. | No; type the full path |
| **Quay.io** | `quay.io` | None for public images, your username and password, or a robot account (`owner+robot`) and its token. | Yes |
| **Amazon ECR** | `<account>.dkr.ecr.<region>.amazonaws.com` | An AWS **access key ID** and **secret access key** allowed `ecr:GetAuthorizationToken` (and `ecr:DescribeRepositories` to search). DocMan fetches ECR's 12-hour registry token itself and renews it as needed. The **AWS region** is read from the address when left empty. | Yes |
| **Google Artifact Registry** | such as `europe-west2-docker.pkg.dev`, or `gcr.io` | A service account's **JSON key** with the Artifact Registry Reader role, pasted in whole. | No; type the full path |
| **Azure Container Registry** | `<name>.azurecr.io` | A service principal's application ID and secret, a repository-scoped token, or the admin user; or an identity (refresh) token. | Yes |
| **GitHub repository** | `github.com`, or your GitHub Enterprise host, plus the **Repository** (`owner/name`, or paste its address) | None for a public repository, or your GitHub username and a personal access token that can read it (fine-grained: *Contents* read-only; classic: `repo`). | Lists the repository's pre-built images; see [GitHub repositories](#github-repositories) |
| **Other registry** | any address, such as Harbor, Nexus, Artifactory or a self-hosted `registry:2` | A username with a password or token, an identity token, or none. | Where the registry allows listing its catalogue |

### GitHub repositories

A **GitHub repository** is not a container registry: it is a repository that holds **pre-built images**, as archives made with `docker save`. DocMan installs and updates only those. A Dockerfile or `docker-compose.yml` in the repository is noticed and mentioned, but DocMan never builds anything from it.

DocMan looks for archives named `.tar`, `.tar.gz` or `.tgz`:

- **in the repository's files**, anywhere, or only in the **Folder** you set (such as `dist`), on the **Branch or tag** you set (the default branch when empty). Files kept in Git LFS work on github.com;
- **attached to its releases**, except drafts and pre-releases.

It reads the name, version and architecture from the file name, in the form `name-version-architecture.tar.gz`, such as `docman-1.9.2-amd64.tar.gz`. The version and architecture are optional. Architectures are written `amd64`, `arm64`, `armv7`, `386` or `multiarch`, and common alternatives such as `x86_64` and `aarch64` are understood. For a release asset without a version in its name, the release's tag is the version.

Only archives that run on this host's architecture can be installed. **Test** on the row says how many archives the repository has, and how many suit this host.

**How updates work.** DocMan remembers which archive each installed tag came from. The daily update check, **Images → Pull** and **Recreate** then look in the repository for a newer version of the same archive (the same name and architecture), download it and load it. An archive replaced in place, with the same name but new content, counts as newer too.

A tag that moves between versions, such as `latest`, is updated this way. A tag that *is* the version, such as `app:1.9.1`, is **pinned**: loading version 1.9.2 adds `app:1.9.2` but leaves `app:1.9.1` as it was, so DocMan reports that a newer version exists without changing it. Create containers from the moving tag if you want them to follow new versions; the wizard picks `latest` for you when the archive has it.

**API address** is only needed for GitHub Enterprise, when its API is not at `https://<host>/api/v3`.

### Sign-in methods

| Method | What DocMan sends |
| --- | --- |
| **No sign-in** | Nothing: only public images can be pulled. |
| **Username and password or access token** | The username and secret, to the registry's own token service. Use an access token rather than your account password wherever the registry offers one. |
| **Identity token** | An OAuth refresh token the registry issued, exchanged for an access token on each use. |
| **AWS access key** | Nothing directly: DocMan asks AWS for a registry token with the key and uses that. |
| **Service-account JSON key** | The key, as Google expects (username `_json_key`). |

Passwords, tokens and keys are stored encrypted, with a key kept in `secrets.key` in DocMan's data folder, and are never shown again or sent back to the browser. To change one, **Edit** the registry and type the new value; leaving the field empty keeps the saved one. A copy of the database without `secrets.key` does not reveal them, so back up both together.

Removing a registry deletes its saved sign-in. Images pulled from it stay on the host; later pulls and update checks for them go without a sign-in.

## Preferences

| Setting | Meaning |
| --- | --- |
| **Theme** | **Dark**, **Light**, or **Match the system**. It takes effect straight away and also applies to this help. |
| **Log lines to load** | How much earlier output the [Logs](/help/logs) tab loads when it opens, between 50 and 10,000 lines. |
| **Statistics interval (ms)** | How often live figures on the Overview and Statistics pages refresh, in milliseconds. Lower is smoother; higher puts less load on the host. |
| **Relying party ID** | The host name passkeys are bound to. Leave it empty to use whatever name the browser used. Changing it makes existing passkeys stop working. See [Passkeys need a hostname](/help/sign-in#passkeys-need-a-hostname). |

**Managed network** shows the name and subnet of the network DocMan uses for [fixed addresses](/help/addresses), once it exists.

Choose **Save preferences** to keep your changes.

## Activity

The activity log records every change made through DocMan, from the UI or the API, newest first: the 300 most recent entries.

| Column | Meaning |
| --- | --- |
| **When** | How long ago. Hover for the exact time. |
| **Who** | The user who made the change, or, for a change made through the API, the name of the token used, marked **API token**. Sign-in attempts are recorded under the username that was tried. |
| **Action** | What was done, such as `container.recreate` or `image.pull`. |
| **Target** | What it was done to. |
| **Detail** | Extra information, such as the error for a failed action. |
| Result | **ok**, or **failed**. |

Reading things, such as opening a page or viewing logs, is not recorded; only changes are.
