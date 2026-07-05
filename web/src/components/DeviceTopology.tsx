// DeviceTopology.tsx — wrapper around NodeNeighbors that takes the device
// id, looks up the linked topology.node_id via GET /devices/:id, and
// renders the relations. Extracted from HostDetail.tsx so EdgeDetail can
// render the same panel keyed off edge.device_id (per T-EdgeTopologyTab-3).
//
// Note: the device row carries node_id directly; we use that rather than
// re-deriving from listDevices. If it's null we render the same
// "not-yet-linked" hint HostDetail already shows.

import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { ApiError } from '@/api/client';
import { useI18n } from '@/i18n/locale';
import { getDevice } from '@/api/devices';
import { NodeNeighbors } from './topology/NodeNeighbors';

type Props = {
  deviceId: number;
};

export function DeviceTopology({ deviceId }: Props) {
  const { tr } = useI18n();
  const [nodeId, setNodeId] = useState<number | null | undefined>(undefined);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setNodeId(undefined);
    setErr(null);
    getDevice(deviceId)
      .then((d) => {
        if (cancelled) return;
        setNodeId(d.node_id ?? null);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : (e as Error).message);
      });
    return () => {
      cancelled = true;
    };
  }, [deviceId]);

  if (err) {
    return (
      <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
        {err}
      </div>
    );
  }
  if (nodeId === undefined) {
    return (
      <div className="flex items-center gap-2 text-xs text-zinc-500">
        <Loader2 size={12} className="animate-spin" />
        {tr('加载拓扑节点…', 'Loading topology node…')}
      </div>
    );
  }
  return (
    <div className="space-y-3">
      <div className="text-xs text-zinc-500">
        {tr(
          '本主机所在的业务拓扑邻居。在 /topology 加成员 / 依赖关系后会出现在下方。',
          'Business topology neighbours of this host. Wire member_of / depends_on edges in /topology and they will appear below.',
        )}
      </div>
      <NodeNeighbors nodeID={nodeId} />
    </div>
  );
}