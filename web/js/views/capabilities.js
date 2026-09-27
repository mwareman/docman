// Linux capabilities: what each one allows, and how a container's cap_add and
// cap_drop lists translate to and from the set of capabilities it ends up with.

/**
 * Every capability, in the order the kernel numbers them. `risk` marks the
 * ones that weaken the isolation between a container and its host.
 */
export const CAPABILITIES = [
  ['CHOWN', 'Change the owner and group of any file.'],
  ['DAC_OVERRIDE', 'Read, write and run any file regardless of its permissions.'],
  ['DAC_READ_SEARCH', 'Read any file and list any directory regardless of permissions, and open files by handle.', 'risk'],
  ['FOWNER', 'Act as the owner of any file: change its permissions, timestamps and flags.'],
  ['FSETID', 'Keep the set-user-ID and set-group-ID bits when a file is changed.'],
  ['KILL', 'Send signals to processes owned by other users.'],
  ['SETGID', 'Switch to any group ID. Needed by programs that drop privileges to a service group.'],
  ['SETUID', 'Switch to any user ID. Needed by programs that start as root and then run as a service user.'],
  ['SETPCAP', 'Pass capabilities on to, or take them from, the programs it starts.'],
  ['LINUX_IMMUTABLE', 'Set or clear the immutable and append-only flags on files.'],
  ['NET_BIND_SERVICE', 'Listen on network ports below 1024, such as 80 and 443, without running as root.'],
  ['NET_BROADCAST', 'Send broadcast and listen to multicast network traffic.'],
  ['NET_ADMIN', 'Configure networking: interfaces, routes, firewall rules and traffic shaping. Needed by VPNs and routers.', 'risk'],
  ['NET_RAW', 'Use raw and packet sockets, for example for ping or packet capture.'],
  ['IPC_LOCK', 'Lock memory so it is never swapped out. Used by databases and some security tools.'],
  ['IPC_OWNER', 'Ignore permission checks on shared memory, message queues and semaphores.'],
  ['SYS_MODULE', 'Load and unload kernel modules, which changes the kernel the whole host runs.', 'risk'],
  ['SYS_RAWIO', 'Direct access to hardware I/O ports and raw devices.', 'risk'],
  ['SYS_CHROOT', 'Use chroot to change the root directory.'],
  ['SYS_PTRACE', 'Trace and inspect other processes, as debuggers and strace do.', 'risk'],
  ['SYS_PACCT', 'Turn process accounting on and off.'],
  ['SYS_ADMIN', 'A broad set of administrative powers, including mounting filesystems. Close to full root on the host.', 'risk'],
  ['SYS_BOOT', 'Reboot the machine and load a new kernel.', 'risk'],
  ['SYS_NICE', 'Raise process priority and change scheduling and CPU affinity for any process.'],
  ['SYS_RESOURCE', 'Go beyond resource limits, such as open files, disk quotas and reserved disk space.'],
  ['SYS_TIME', 'Set the system clock, which is shared by the whole host. Needed by NTP servers.', 'risk'],
  ['SYS_TTY_CONFIG', 'Configure terminal devices and hang up virtual terminals.'],
  ['MKNOD', 'Create device files.'],
  ['LEASE', 'Take leases on files, to be told when another process opens them.'],
  ['AUDIT_WRITE', 'Write records to the kernel audit log. Used by programs that handle logins, such as sshd and su.'],
  ['AUDIT_CONTROL', 'Change kernel auditing rules and turn auditing on and off.', 'risk'],
  ['SETFCAP', 'Set capabilities on program files.'],
  ['MAC_OVERRIDE', 'Override mandatory access control, such as Smack.', 'risk'],
  ['MAC_ADMIN', 'Change mandatory access control settings.', 'risk'],
  ['SYSLOG', 'Read and clear the kernel message log.'],
  ['WAKE_ALARM', 'Set timers that wake the system from suspend.'],
  ['BLOCK_SUSPEND', 'Prevent the system from suspending.'],
  ['AUDIT_READ', 'Read the kernel audit log.'],
  ['PERFMON', 'Use performance monitoring and tracing tools, such as perf.'],
  ['BPF', 'Load eBPF programs into the kernel.', 'risk'],
  ['CHECKPOINT_RESTORE', 'Checkpoint and restore processes, as CRIU does.'],
].map(([name, description, risk]) => ({ name, description, risk: risk === 'risk' }));

/** The capabilities Docker grants every container unless told otherwise. */
export const DOCKER_DEFAULTS = [
  'CHOWN', 'DAC_OVERRIDE', 'FOWNER', 'FSETID', 'KILL', 'SETGID', 'SETUID', 'SETPCAP',
  'NET_BIND_SERVICE', 'NET_RAW', 'SYS_CHROOT', 'MKNOD', 'AUDIT_WRITE', 'SETFCAP',
];

const KNOWN = new Set(CAPABILITIES.map((c) => c.name));
const DEFAULTS = new Set(DOCKER_DEFAULTS);

/** Docker accepts net_admin, NET_ADMIN and CAP_NET_ADMIN alike. */
export function normalizeCap(name) {
  return String(name || '').trim().toUpperCase().replace(/^CAP_/, '');
}

/**
 * The capabilities a container ends up with, given its cap_add and cap_drop.
 * Docker starts from its defaults (or from nothing when ALL is dropped),
 * removes what is dropped, then adds what is added.
 */
export function effectiveCaps(capAdd, capDrop) {
  const add = (capAdd || []).map(normalizeCap).filter(Boolean);
  const drop = (capDrop || []).map(normalizeCap).filter(Boolean);
  const set = new Set(drop.includes('ALL') ? [] : DOCKER_DEFAULTS);
  for (const name of drop) set.delete(name);
  if (add.includes('ALL')) for (const name of KNOWN) set.add(name);
  for (const name of add) if (name !== 'ALL') set.add(name);
  return set;
}

/** Turn a chosen set back into the cap_add and cap_drop Docker expects. */
export function capsToLists(chosen) {
  const add = [...chosen].filter((name) => !DEFAULTS.has(name)).sort().map((name) => `CAP_${name}`);
  const drop = DOCKER_DEFAULTS.filter((name) => !chosen.has(name)).map((name) => `CAP_${name}`);
  return { cap_add: add, cap_drop: drop };
}

export function sameSet(a, b) {
  if (a.size !== b.size) return false;
  for (const item of a) if (!b.has(item)) return false;
  return true;
}

/** Capabilities in a container's settings that DocMan has no description for. */
export function unknownCaps(capAdd, capDrop) {
  return [...new Set([...(capAdd || []), ...(capDrop || [])].map(normalizeCap))]
    .filter((name) => name && name !== 'ALL' && !KNOWN.has(name));
}

export function isDefaultCap(name) {
  return DEFAULTS.has(name);
}
