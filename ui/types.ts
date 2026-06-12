// JSON shapes returned by the hostek Go backend (field names match its json tags).

export interface DiskUsage {
  mount: string;
  fstype: string;
  total: number;
  used: number;
  free: number;
  percent: number;
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
  load1: number;
  load5: number;
  load15: number;
  uptime: number;
  procs: number;
}

export interface Sample {
  time: number;
  cpu: number;
  mem: number;
  netRx: number;
  netTx: number;
  disk: number;
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
  biosAutoPowerOn: BiosNote;
}
