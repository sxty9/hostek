// JSON shapes returned by the hostek Go backend (field names match its json tags).

export interface DiskUsage {
  mount: string;
  fstype: string;
  total: number;
  used: number;
  free: number;
  percent: number;
}

export interface GPU {
  index: number;
  name: string;
  utilPercent: number;
  memUsed: number;
  memTotal: number;
  memPercent: number;
  tempC: number;
  powerW: number;
}

// Value averaged over 1/5/15 min (EWMA), the per-component analogue of load average.
export interface Avg {
  a1: number;
  a5: number;
  a15: number;
}
export interface Loads {
  cpu: Avg;
  mem: Avg;
  gpu: Avg;
  ssd: Avg;
  net: Avg;
}

export interface Summary {
  time: number;
  cpuPercent: number;
  perCpu: number[];
  memTotal: number;
  memUsed: number;
  memAvailable: number;
  memCached: number;
  memPercent: number;
  swapTotal: number;
  swapUsed: number;
  swapPercent: number;
  disks: DiskUsage[];
  netRxRate: number;
  netTxRate: number;
  gpus?: GPU[];
  sysDiskDevice?: string;
  sysDiskReadRate: number;
  sysDiskWriteRate: number;
  sysDiskBusyPercent: number;
  load1: number;
  load5: number;
  load15: number;
  loads: Loads;
  uptime: number;
  procs: number;
}

export interface PowerSample {
  time: number;
  cpu: number;
  gpu: number;
  total: number;
}
export interface PowerAvg {
  cpu: Avg;
  gpu: Avg;
  total: Avg;
}
export interface PowerResponse {
  samples: PowerSample[];
  avg: PowerAvg;
  cpuAvailable: boolean;
  gpuAvailable: boolean;
}

export interface ThermalMeta {
  key: string;
  label: string;
  source: string;
  criticalC: number;
}
export interface ThermalSample {
  time: number;
  temps: Record<string, number>;
}
export interface ThermalResponse {
  components: ThermalMeta[];
  samples: ThermalSample[];
}

export interface Sample {
  time: number;
  cpu: number;
  mem: number;
  gpu: number;
  ssdBusy: number;
  ssdRead: number;
  ssdWrite: number;
  netRx: number;
  netTx: number;
}
export interface SeriesResponse {
  samples: Sample[];
}

export interface Process {
  pid: number;
  name: string;
  user: string;
  cpuPercent: number;
  memRss: number;
  memPercent: number;
  gpuPercent: number;
  gpuEngine?: string;
  gpuMem?: number;
  netRxRate: number;
  netTxRate: number;
  status: string;
}
export interface ProcessesResponse {
  processes: Process[];
}

export interface HostInfo {
  hostname: string;
  os: string;
  platform: string;
  platformVersion: string;
  kernel: string;
  arch: string;
  cpuModel: string;
  cpuCores: number;
  cpuThreads: number;
  memTotal: number;
  bootTime: number;
}

// ── Hardware inventory (System tab) ──────────────────────────────────────────────
export interface CPUInfo {
  model?: string;
  vendor?: string;
  socket?: string;
  cores?: number;
  threads?: number;
  family?: string;
  baseClockMhz?: number;
  maxClockMhz?: number;
  curClockMhz?: number;
  perCoreMhz?: number[];
  tempC?: number;
  cacheL1?: string;
  cacheL2?: string;
  cacheL3?: string;
}

export interface MemoryModule {
  slot?: string;
  sizeBytes?: number;
  type?: string;
  speedMhz?: number;
  configuredMhz?: number;
  manufacturer?: string;
  partNumber?: string;
  rank?: string;
  timings?: string;
}

export interface MemoryInfo {
  totalBytes?: number;
  modules?: MemoryModule[];
}

export interface BoardInfo {
  manufacturer?: string;
  model?: string;
  version?: string;
  biosVendor?: string;
  biosVersion?: string;
  biosDate?: string;
}

export interface GPUInfo {
  name?: string;
  memTotalBytes?: number;
  driver?: string;
  cuda?: string;
  baseClockMhz?: number;
  boostClockMhz?: number;
  curClockMhz?: number;
  memClockMhz?: number;
  memMaxClockMhz?: number;
  tempC?: number;
  powerW?: number;
  powerLimitW?: number;
}

export interface DiskHWInfo {
  device?: string;
  model?: string;
  serial?: string;
  firmware?: string;
  sizeBytes?: number;
  type?: string;
  health?: string;
  tempC?: number;
  powerOnHours?: number;
}

export interface NICInfo {
  name?: string;
  model?: string;
  mac?: string;
  speedMbps?: number;
  driver?: string;
  link?: string;
}

export interface HardwareInfo {
  hostname?: string;
  cpu: CPUInfo;
  memory: MemoryInfo;
  board: BoardInfo;
  gpus?: GPUInfo[];
  disk: DiskHWInfo;
  nics?: NICInfo[];
}

// ── Disks tab (all devices) ──────────────────────────────────────────────────────
export interface DiskPartition {
  name: string;
  mount?: string;
  fstype?: string;
  sizeBytes: number;
  used?: number;
  total?: number;
  percent?: number;
}

export interface DiskDevice {
  name: string;
  model?: string;
  serial?: string;
  transport?: string;
  port?: string;
  sizeBytes: number;
  rotational: boolean;
  type?: string;
  isSystem: boolean;
  health?: string;
  tempC?: number;
  firmware?: string;
  powerOnHours?: number;
  partitions?: DiskPartition[];
}
export interface DisksResponse {
  disks: DiskDevice[];
}

export interface BiosNote {
  setting: string;
  value: string;
  note: string;
}
export interface PowerState {
  platform: string;
  supported: boolean;
  headless: boolean;
  lidIgnore: boolean;
  suspendMasked: boolean;
  tmuxPersist: boolean;
  tmuxResume: boolean;
  biosAutoPowerOn: BiosNote;
}
