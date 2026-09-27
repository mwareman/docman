# Fixed addresses

Docker gives containers on its default network whatever address is free when they start, so a container's address can change after a restart or a reboot. When another system needs to reach a container at a known address, such as a firewall rule, a reverse proxy, or another container configured with an IP, give it a **fixed address**.

## Fixing an address

From the container: open it and choose **Fix this address** in the **Network** card on the Overview tab.

From the Addresses page: choose **Fix an address**, then pick the container.

Either way, leave **Address** empty to take the next free one, or type a specific address from DocMan's range. When you create a container you can do the same thing with **Give this container a fixed internal address** in the [deploy wizard](/help/deploy#step-3-optionally-fix-the-address).

The container keeps running while it moves, but it gets a new address, so existing connections to its old address are broken.

## How it works

DocMan keeps one Docker network of its own for fixed addresses, called `docman0`. You never have to create it:

- The first time you fix an address, DocMan picks a private subnet that does not clash with any existing Docker network or with the host's own interfaces. It tries `172.20.0.0/16` first, then `172.21.0.0/16` and so on.
- It splits the subnet in two. DocMan hands out fixed addresses from the lower half, starting at `.10`. Docker keeps the upper half for containers that join the network without a fixed address.
- It attaches the container to `docman0` at the chosen address and takes it off Docker's default `bridge` network. Other networks you attached it to yourself are left alone.
- It remembers the address by container name, so it is reapplied whenever the container is recreated or its configuration changes.

Containers on `docman0` can also reach each other by name: a container called `db` answers to the host name `db` for the others.

## The Addresses page

**Addresses** in the menu shows:

- **DocMan address pool**: the network's name, subnet, gateway, the range DocMan allocates from and the range left to Docker. Before your first fixed address it reads **not created yet**.
- **Fixed addresses**: every reservation, with its container, network, state and when it was made.
- **Docker networks**: every network on the host, with its driver, subnet and number of containers. DocMan's own is marked **DocMan**.

A reservation's state is one of:

| State | Meaning | What to do |
| --- | --- | --- |
| **in place** | The container is at its fixed address. | Nothing. |
| **drifted** | The container exists but is at a different address. Hover over the badge to see which. | Choose **Re-apply** on its row. |
| **container missing** | No container of that name exists any more. | Create one with that name to reuse the address, or release it. |

**Re-apply addresses** at the top of the page checks every reservation and repairs any that drifted. DocMan also does this by itself every time it starts.

### If DocMan loses its records

The reservations live in DocMan's database, in its `/data` volume. If that database is lost or replaced, for example when DocMan is moved to a new data volume or restored from an old backup, the containers keep their addresses, but DocMan no longer knows they were fixed.

DocMan rebuilds the records by itself. Every container attached to its `docman0` network with an address that was asked for explicitly can only have been given it by DocMan, so each start (and **Re-apply addresses**) records those again. The log shows a `recovered the fixed address` line for each one. Existing records are never changed.

This works as long as the containers are still on `docman0`. A container whose address was released, or that was moved to another network, has nothing to recover from.

## Releasing an address

Choose **Release fixed address** on the container's Network card, or the ✕ button on its row on the Addresses page. The container leaves `docman0` and goes back to Docker's default network with an automatic address. The address becomes free for another container.

Removing a container also releases its address.

> **Note:** A container that shares the host's network (network mode `host`), has networking disabled (`none`), or shares another container's network cannot have a fixed address. It has no address of its own.
