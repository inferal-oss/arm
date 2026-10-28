// Operator-facing explanations are deliberately separate from collector IDs.
// Backend strings remain data and are only shown in the technical disclosure.
export function metricHelp(kind, {measurements = [], filesystem, accounting, cpu, memory, disk} = {}) {
  const copy = {
    cpu: ['CPU activity', 'How busy the host’s processors have been since the previous sample. This includes work outside ARM.', 'A core count reserves capacity in ARM; it does not guarantee a particular speed.'],
    memory: ['Memory & swap', 'RAM remaining is the system’s current estimate of available physical memory, including supported reclaimable caches. Swap remaining is available swap space on disk.', 'These host readings are separate from ARM’s remaining reservable capacity after lease promises. See Resources for what ARM can lease.'],
    filesystem: ['Storage space', 'Space remaining is currently available to your user on the filesystem where ARM stores its data. Other disks and volumes may have different limits.', 'This physical reading is separate from ARM’s remaining reservable disk budget after lease promises. File size is not the same as space that deletion will free: snapshots, clones and shared files can retain data.'],
    disk_io: ['Disk activity', 'Read and write rates add the reports from every selected device, including work outside ARM. This is a sum of reported counters, not exclusive physical throughput.', 'Virtual devices and their underlying drives can report the same activity. Adding their rates can count it twice.'],
    network: ['Network activity', 'Receive and send rates add the reports from every selected interface, including work outside ARM. This is a sum of reported counters, not exclusive physical throughput.', 'A VPN or virtual interface may report traffic also counted by another interface. The rates are not bandwidth reservations.'],
    gpu: ['GPU monitoring', 'GPU activity is not available in this build. An unavailable reading does not mean the GPU is idle.'],
    'resource-cpu': ['Live CPU headroom', 'The CPU pool combines recently idle processor time and conservatively verified use from current ARM jobs, up to the effective CPU limits and operator ceiling. Other work on the host can shrink that headroom.', 'ARM subtracts full outstanding CPU promises. Aligned native observations can credit part of the usage already included in those promises. Unverified work, unsupported accounting scopes and rounding margins still receive no credit, which can make new jobs wait.', 'CPU reservations are cooperative promises, not operating-system limits. Logical core counts do not compare processor speed across hosts.'],
    'resource-memory': ['Memory reservations', 'Available is the portion of this memory pool not promised to current leases. Physical RAM and actual usage are shown separately.', 'A reservation is cooperative and does not guarantee that the operating system can satisfy an allocation.'],
    'resource-storage': ['Storage location pools', 'A configured path selects where ARM creates managed storage. Compare measured write throughput and available capacity when choosing a pool for write-heavy work.', 'Tier labels are operator hints. Benchmark age, cache method, current contention and shared backing matter; overlapping pool capacities must not be added. Performance is advisory, without an I/O guarantee.'],
    'resource-disk': ['Storage reservations', 'The pool is ARM’s storage budget. Available means the portion not promised to current leases.', 'This is separate from free disk space. Shared blocks or snapshots can keep storage charged after files are removed.'],
    'resource-port': ['Port reservations', 'Each claim records its address, protocol and consumer. Holding a socket reserves its binding; a cooperative claim relies on the consumer to release it.'],
    'resource-custom': ['Coordination pool', 'This pool coordinates a limited number of shared permits. Its capacity and changes are recorded by ARM.', 'Permits do not enforce limits on an external system. A capacity reduction can leave existing claims above the new pool size.'],
  };
  const [title, ...paragraphs] = copy[kind] ?? ['Resource information', 'This reading describes one reported resource.'];
  if (kind === 'disk_io' || kind === 'network') paragraphs.push('Select all includes newly reported devices. A custom selection keeps exact device identities. Missing, unavailable or reset counters leave gaps rather than adding zero; a changed device set also starts a new combined history segment.');
  if (kind === 'filesystem' && filesystem?.shared) {
    paragraphs.push(filesystem.type === 'apfs'
      ? 'APFS volumes can share one storage container. Their totals describe shared space and must not be added together.'
      : 'This filesystem shares an underlying storage pool. Its total may overlap with other volumes.');
  }
  if (kind === 'resource-memory' && memory) {
    paragraphs[0] = 'The pool follows the system’s current estimate of available RAM, up to an optional operator ceiling. It can shrink while leases are active.';
    paragraphs.push('ARM currently subtracts full outstanding promises. It cannot yet safely credit shared or compressed memory already used by those jobs, so this can reserve more headroom than necessary.');
  }
  if (kind === 'resource-cpu' && !cpuBudgetKnown(cpu)) paragraphs.push('CPU availability is missing or stale. New CPU grants wait; existing promises stay charged. Unknown does not mean the host is idle.');
  if (kind === 'resource-disk' && disk) {
    paragraphs[0] = 'The pool follows space currently available to your user on ARM’s state filesystem, up to an optional operator ceiling. It can shrink while leases are active.';
    paragraphs[1] = 'ARM subtracts full outstanding disk promises, even when those jobs have already written files. This is conservative headroom, not exclusive free space or a hard disk quota.';
    paragraphs.push('APFS volumes may share one container. Snapshots and clones can retain blocks after files are removed; ARM does not count them as guaranteed reclamation. Other mounted filesystems have separate limits.');
    if (!diskBudgetKnown(disk)) paragraphs.push('The latest availability reading is missing or stale. New disk grants wait; outstanding promises and ordinary cleanup remain visible.');
  }
  const technical = [];
  if (kind === 'resource-cpu' && cpu?.observation) {
    const o=cpu.observation;
    for(const [label,value] of [['Scope',o.scope],['Source',o.method],['Quality',o.quality],['Measured at',o.at],['Sample interval (ns)',o.interval_ns],['Eligible logical CPUs',o.eligible_cpus],['Collector note',o.detail]]) if(value!==undefined&&value!==null&&value!=='')technical.push([`Availability · ${label}`,String(value)]);
  }
  if (kind === 'resource-disk' && disk?.observation) {
    const o = disk.observation;
    for (const [label, value] of [['Scope',o.scope],['Filesystem',o.filesystem],['Source',o.method],['Quality',o.quality],['Measured at',o.at],['Collector note',o.detail]]) if (value) technical.push([`Availability · ${label}`, String(value)]);
  }
  for (const [label, m] of measurements) {
    if (!m) continue;
    for (const [key, value] of [['Scope', m.scope], ['Source', m.method], ['Quality', m.quality], ['Measured at', m.at], ['Collector note', m.detail]]) {
      if (value) technical.push([`${label} · ${key}`, String(value)]);
    }
  }
  if (filesystem) {
    for (const [key, value] of [['Filesystem', filesystem.type], ['Filesystem note', filesystem.detail]]) {
      if (value) technical.push([key, String(value)]);
    }
  }
  if (accounting) technical.push(['Accounting', String(accounting)]);
  return {title, paragraphs, technical};
}

export function cpuBudgetKnown(cpu) {
  const finite=v=>typeof v==='number'&&Number.isFinite(v)&&v>=0;
  return cpu?.known === true && cpu.stale !== true && cpu.observation?.known === true && [cpu.observation.available_cores,cpu.capacity_cores,cpu.reserved_cores,cpu.available_cores,cpu.deficit_cores].every(finite);
}

export function cpuCores(value) {
  if(value==null)return 'unavailable';
  const n=Number(value);
  return Number.isFinite(n)&&n>=0?(n>0&&n<.01?'<0.01':n.toLocaleString('en-US',{maximumFractionDigits:2}))+' cores':'unavailable';
}

// Add time spent holding a status snapshot; no comparison with a remote clock.
export function budgetForView(budget,{connected=true,elapsedMS=0}={}){
  if(!budget)return null;
  const age=Number(budget.age_ns)/1e6+Math.max(0,elapsedMS),after=Number(budget.stale_after_ns)/1e6;
  return {...budget,stale:budget.stale||!connected||!Number.isFinite(age)||age<0||!Number.isFinite(after)||after<=0||age>=after};
}
export const cpuBudgetForView = budgetForView;

export function capacityBudgetsForView(status, options) {
  return {cpu:budgetForView(status?.cpu_budget,options),memory:budgetForView(status?.memory_budget,options),disk:budgetForView(status?.disk_budget,options)};
}

export function memoryBudgetKnown(memory) {
  const total=Number(memory?.observation?.total_bytes),available=Number(memory?.observation?.available_bytes);
  return memory != null && !memory.stale && memory.observation?.total_bytes != null && memory.observation?.available_bytes != null && Number.isFinite(total) && total>0 && Number.isFinite(available) && available>=0 && available<=total;
}

export function diskBudgetKnown(disk) { return disk?.known === true && disk.stale !== true; }

export function capacityFreshnessKey({cpu,memory,disk}) {
  return `${Number(cpuBudgetKnown(cpu))}${Number(memoryBudgetKnown(memory))}${Number(diskBudgetKnown(disk))}`;
}

export function memoryBudgetFields(resource, memory) {
  const known = memoryBudgetKnown(memory);
  return [['TO LEASE', known ? resource.available : null], ['LIVE POOL', known ? resource.budget_total : null], ['PROMISED', resource.reserved], ['SHORTFALL', known ? resource.deficit : null]];
}

export function remoteCPUBudget(cpu){
  if(!cpu)return null;
  return {...cpu,observation:{known:cpu.known,at:cpu.observation_at,eligible_cpus:cpu.eligible_cpus,capacity_cores:cpu.observed_capacity_cores,busy_cores:cpu.busy_cores,available_cores:cpu.observed_available_cores,quality:cpu.quality}};
}

// A live zero is distinct from unknown; full promises remain visible in both.
export function cpuBudgetFields(resource, cpu) {
  const known=cpuBudgetKnown(cpu);
  return [['TO LEASE',known?cpu.available_cores:null],['LIVE HEADROOM',known?cpu.capacity_cores:null],['PROMISED',cpu?.reserved_cores??resource.reserved],['SHORTFALL',known?cpu.deficit_cores:null]];
}

// Unknown effective admission capacity is deliberately unavailable in the UI,
// while full outstanding promises remain visible (never a false idle zero).
export function diskBudgetFields(resource, disk) {
  const known = diskBudgetKnown(disk);
  return [
    ['TO LEASE', known ? resource.available : null],
    ['LIVE POOL', known ? resource.budget_total : null],
    ['PROMISED', resource.reserved],
  ];
}
