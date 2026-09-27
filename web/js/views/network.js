// Fixed addresses: the network DocMan manages, and every container pinned to it.

import {
  h, add, clear, icon, when, toast, toastError, confirmDialog, modal,
  field, notice, spinner, emptyState, copyText,
} from '../ui.js';
import { Networks, Containers } from '../api.js';

export function networkView(ctx) {
  ctx.setCrumbs('Addresses');

  const host = h('div', { class: 'col', style: { gap: '14px' } });
  add(ctx.host, [host]);
  add(host, [spinner()]);

  ctx.setActions(
    h('button', {
      class: 'btn', onClick: async () => {
        try {
          const result = await Networks.reconcile();
          toast(result.notes.length ? result.notes.join('; ') : 'Every fixed address is already in place', 'ok', 8000);
          load();
        } catch (err) { toastError(err); }
      },
    }, icon('refresh'), 'Re-apply addresses'),
    h('button', { class: 'btn primary', onClick: () => pinSomething() }, icon('pin'), 'Fix an address'),
  );

  async function pinSomething() {
    let containers = [];
    try {
      containers = (await Containers.list()).containers || [];
    } catch (err) {
      toastError(err);
      return;
    }
    const candidates = containers.filter((c) => !c.pinned_ip);
    const chosen = await modal({
      title: 'Fix a container’s address',
      confirmLabel: 'Fix address',
      render: ({ body }) => {
        const select = h('select', {}, ...candidates.map((c) =>
          h('option', { value: c.id, text: `${c.name}${c.primary_ip ? ` — currently ${c.primary_ip}` : ''}` })));
        const ip = h('input', { type: 'text', class: 'mono', placeholder: 'leave empty for the next free address' });
        add(body, [
          candidates.length
            ? h('p', { class: 'muted', style: { margin: 0 } },
              'The container is attached to DocMan’s bridge network at the address you choose and taken off the default bridge, so the address stops changing.')
            : notice('Every container already has a fixed address.', 'info'),
          candidates.length ? field('Container', select) : null,
          candidates.length ? field('Address', ip) : null,
        ]);
        if (!candidates.length) return null;
        return { submit: () => ({ id: select.value, ip: ip.value.trim() }) };
      },
    });
    if (!chosen || !chosen.id) return;
    try {
      const result = await Containers.pin(chosen.id, chosen.ip);
      toast(`${result.container} is now at ${result.ip}`, 'ok');
      if (result.note) toast(result.note, 'warn', 9000);
      load();
    } catch (err) { toastError(err); }
  }

  async function load() {
    clear(host);
    add(host, [spinner()]);
    let data;
    let networks = [];
    try {
      data = await Networks.addresses();
      networks = (await Networks.list()).networks || [];
    } catch (err) {
      clear(host);
      add(host, [notice(err.message, 'bad')]);
      return;
    }
    clear(host);

    const plan = data.network;
    add(host, [
      h('div', { class: 'card' },
        h('div', { class: 'card-head' }, icon('network'), h('h2', { text: 'DocMan address pool' }),
          h('div', { class: 'grow' }),
          plan ? h('span', { class: 'badge ok', text: 'provisioned' }) : h('span', { class: 'badge plain', text: 'not created yet' })),
        h('div', { class: 'card-body' }, plan ? planBody(plan) : h('div', { class: 'col' },
          h('p', { class: 'muted', style: { margin: 0 } },
            'Docker hands out addresses on its default bridge in the order containers start, so they move around. '
            + 'DocMan solves that by keeping a bridge network of its own with a known subnet: the first time you fix an '
            + 'address it picks a free private range, creates the network, and moves the container onto it.'),
          h('div', { class: 'small faint' }, 'Nothing is created until you fix your first address.'),
        )),
      ),
      addressCard(data.addresses || []),
      networksCard(networks),
    ]);
  }

  function planBody(plan) {
    const kv = h('dl', { class: 'kv' });
    const rows = [
      ['Network', h('span', { class: 'mono' }, plan.name)],
      ['Subnet', h('span', { class: 'mono' }, plan.subnet)],
      ['Gateway', h('span', { class: 'mono' }, plan.gateway)],
      ['Fixed range', h('span', {},
        h('span', { class: 'pill accent', text: `${plan.static_first} – ${plan.static_last}` }),
        h('span', { class: 'small faint', style: { marginLeft: '8px' }, text: 'DocMan allocates from here' }))],
      ['Automatic range', h('span', {},
        h('span', { class: 'pill', text: plan.dynamic_from }),
        h('span', { class: 'small faint', style: { marginLeft: '8px' }, text: 'left to docker' }))],
    ];
    for (const [key, value] of rows) add(kv, [h('dt', { text: key }), h('dd', {}, value)]);
    return kv;
  }

  function addressCard(addresses) {
    const body = h('tbody');
    const card = h('div', { class: 'card' },
      h('div', { class: 'card-head' }, icon('pin'), h('h2', { text: 'Fixed addresses' }),
        h('div', { class: 'grow' }),
        h('span', { class: 'small faint', text: `${addresses.length} reserved` })),
      addresses.length
        ? h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
          h('thead', {}, h('tr', {},
            h('th', { text: 'Address' }),
            h('th', { text: 'Container' }),
            h('th', { text: 'Network' }),
            h('th', { text: 'State' }),
            h('th', { text: 'Reserved' }),
            h('th', { class: 'actions' }),
          )),
          body,
        ))
        : emptyState('No fixed addresses yet', 'Fix one from a container’s Network panel, or with the button above.'),
    );

    add(body, addresses.map((entry) => h('tr', {},
      h('td', {}, h('div', { class: 'row' },
        h('span', { class: 'pill accent', text: entry.ip }),
        h('button', {
          class: 'btn sm ghost icon', title: 'Copy address',
          onClick: () => copyText(entry.ip, 'Address copied'),
        }, icon('copy')),
      )),
      h('td', {}, h('a', { href: `#/containers/${encodeURIComponent(entry.container)}`, text: entry.container })),
      h('td', { class: 'mono small', text: entry.network }),
      h('td', {}, stateBadge(entry)),
      h('td', { class: 'small faint', text: when(entry.created_at) }),
      h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
        entry.exists && !entry.in_sync
          ? h('button', {
            class: 'btn sm', title: 'Re-apply this address',
            onClick: async () => {
              try {
                await Containers.pin(entry.container, entry.ip);
                toast('Address re-applied', 'ok');
                load();
              } catch (err) { toastError(err); }
            },
          }, icon('refresh'), 'Re-apply')
          : null,
        h('button', {
          class: 'btn sm icon danger', title: 'Release',
          onClick: async () => {
            const ok = await confirmDialog({
              title: `Release ${entry.ip}?`,
              message: `${entry.container} goes back to a docker-assigned address on the default bridge.`,
              confirmLabel: 'Release',
            });
            if (!ok) return;
            try {
              await Containers.unpin(entry.container);
              toast('Address released', 'ok');
              load();
            } catch (err) { toastError(err); }
          },
        }, icon('x')),
      )),
    )));
    return card;
  }

  function stateBadge(entry) {
    if (!entry.exists) return h('span', { class: 'badge warn', text: 'container missing' });
    if (!entry.in_sync) {
      return h('span', {
        class: 'badge bad',
        title: `live address is ${entry.live_ip || 'unknown'}`,
        text: 'drifted',
      });
    }
    return h('span', { class: 'badge ok', text: 'in place' });
  }

  function networksCard(networks) {
    const body = h('tbody');
    add(body, networks.map((net) => h('tr', {},
      h('td', {}, h('div', { class: 'row' },
        h('span', { class: 'mono', text: net.Name }),
        net.managed ? h('span', { class: 'badge info', text: 'DocMan' }) : null,
      )),
      h('td', { text: net.Driver || '—' }),
      h('td', { class: 'mono small', text: net.subnet || '—' }),
      h('td', { class: 'num', text: String(Object.keys(net.Containers || {}).length) }),
      h('td', {}, net.Internal ? h('span', { class: 'badge plain', text: 'internal' }) : h('span', { class: 'faint small', text: '—' })),
    )));
    return h('div', { class: 'card' },
      h('div', { class: 'card-head' }, icon('link'), h('h2', { text: 'Docker networks' })),
      h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {},
          h('th', { text: 'Name' }),
          h('th', { text: 'Driver' }),
          h('th', { text: 'Subnet' }),
          h('th', { class: 'num', text: 'Containers' }),
          h('th', { text: '' }),
        )),
        body,
      )),
    );
  }

  load();
  return { dispose() {} };
}
