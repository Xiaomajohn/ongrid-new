// installjob.ts — thin re-export of the install-job wire functions so
// pages / modals can import them without pulling in the whole device
// API surface. The implementations live in `./devices.ts` next to the
// other device-scoped endpoints; keeping the re-export here lets us
// split the panel off (and lazy-import the binary `fs*` helpers) later
// without churning call sites.

export type {
  InstallJob,
  InstallJobStatus,
  InstallEdgeOptions,
  InstallEdgeResponse,
} from './devices';

export {
  installEdge,
  getInstallJob,
  listInstallJobsByDevice,
  listInstallJobsByEdge,
  cancelInstallJob,
} from './devices';