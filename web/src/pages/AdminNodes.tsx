import { useState } from "react";
import type { FormEvent, ReactNode } from "react";
import type { StreamNode, CreateNodeRequest, UpdateNodeRequest } from "@/api/types";
import {
  useAdminNodes,
  useCreateNode,
  useUpdateNode,
  useDeleteNode,
  useCheckNodeHealth,
  useReprobeNode,
  useToggleNode,
} from "@/hooks/queries/admin/nodes";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Plus, Pencil, Trash2, RefreshCw, ScanSearch, Info, AlertTriangle } from "lucide-react";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { formatDateTime } from "@/lib/datetime";
import { toggleHWDevice } from "@/lib/hwDevices";
import { cn } from "@/lib/utils";
import type { NodeHWDeviceRow, ResourceMetric } from "./adminNodesPresentation";
import {
  HW_ACCEL_INHERIT,
  HW_ACCEL_OVERRIDE_OPTIONS,
  buildNodeHWDeviceRows,
  describeCapabilityDrift,
  describeEffectiveAcceleration,
  describeNodeAccelerationOverride,
  describeNodeGPU,
  describeNodeSystem,
  describeSharedGPU,
  nodeHWDevicePaths,
  nodeHasHWDeviceInventory,
  nodeUsesCUDADevices,
  parseHWDeviceOverride,
} from "./adminNodesPresentation";

type NodeType = "proxy" | "transcode";

function formatMbps(kbps: number): string {
  return (Math.round(kbps / 100) / 10).toString();
}

/** One derived reading in the System column: "CPU 42%", muted or tinted. */
function NodeSystemMetric({ metric }: { metric: ResourceMetric }) {
  return (
    <span className="inline-flex items-baseline gap-1 whitespace-nowrap" title={metric.title}>
      <span className="text-muted-foreground">{metric.label}</span>
      <span
        className={cn(
          "tabular-nums",
          metric.muted && "text-muted-foreground",
          metric.warning && "text-warning font-medium",
        )}
      >
        {metric.value}
      </span>
    </span>
  );
}

function NodeSystemCell({ node }: { node: StreamNode }) {
  const system = describeNodeSystem(node);
  if (system.kind === "unreported") {
    return (
      <span className="text-muted-foreground text-sm" title={system.title}>
        {system.label}
      </span>
    );
  }

  return (
    <div className="space-y-0.5 text-xs">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <NodeSystemMetric metric={system.cpu} />
        <NodeSystemMetric metric={system.memory} />
      </div>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <NodeSystemMetric metric={system.disk} />
        <NodeSystemMetric metric={system.network} />
      </div>
    </div>
  );
}

/** The "override: qsv" line, or nothing on a node that inherits the cluster. */
function NodeOverrideLine({ node }: { node: StreamNode }) {
  const override = describeNodeAccelerationOverride(node);
  if (!override) {
    return null;
  }
  return (
    <div className="text-muted-foreground text-xs" title={override.title}>
      {override.label}
    </div>
  );
}

/**
 * The "Shared GPU" marker, or nothing when this node's card is its own. Muted
 * rather than tinted: sharing hardware is information an operator needs when
 * reading job counts, not a fault.
 */
function NodeSharedGPUBadge({ node, allNodes }: { node: StreamNode; allNodes: StreamNode[] }) {
  const shared = describeSharedGPU(node, allNodes);
  if (!shared) {
    return null;
  }
  return (
    <Badge
      variant="outline"
      className="bg-surface text-muted-foreground border-border"
      title={shared.title}
    >
      {shared.label}
    </Badge>
  );
}

/**
 * The "Drift" marker on a node whose last capability refetch found its hardware
 * got worse. Tinted, unlike the shared-GPU marker: this one is a regression an
 * operator has to act on, and the Health column will not show it.
 */
function NodeDriftBadge({ node }: { node: StreamNode }) {
  const drift = describeCapabilityDrift(node);
  if (!drift) {
    return null;
  }
  return (
    <Badge
      variant="outline"
      className="bg-warning/10 text-warning border-warning/15"
      title={drift.title}
    >
      {drift.label}
    </Badge>
  );
}

function NodeGPUCell({ node, allNodes }: { node: StreamNode; allNodes: StreamNode[] }) {
  const gpu = describeNodeGPU(node);
  if (gpu.kind === "awaiting") {
    return (
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-muted-foreground text-sm" title={gpu.title}>
            {gpu.label}
          </span>
          <NodeDriftBadge node={node} />
        </div>
        <NodeOverrideLine node={node} />
      </div>
    );
  }

  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant="outline" className={gpu.backend.badgeClass} title={gpu.backend.title}>
          {gpu.backend.label}
        </Badge>
        <NodeDriftBadge node={node} />
        <NodeSharedGPUBadge node={node} allNodes={allNodes} />
        {gpu.failures.length > 0 && (
          <span
            className="text-warning inline-flex"
            title={gpu.failures.map((failure) => `${failure.label}: ${failure.reason}`).join("\n")}
          >
            <AlertTriangle
              className="h-3.5 w-3.5"
              aria-label={`${gpu.failures.length} hardware backend probe failure(s) on ${node.name}`}
            />
          </span>
        )}
        {gpu.stale && (
          <span
            className="text-muted-foreground text-xs"
            title={
              `No health check has confirmed this inventory since ${formatDateTime(node.last_health_check ?? "")}. ` +
              `It was last refreshed ${formatDateTime(node.capabilities_refreshed_at ?? "")}.`
            }
          >
            stale
          </span>
        )}
      </div>
      {gpu.deviceSummary && (
        <div className="text-muted-foreground text-xs" title={gpu.deviceTitle ?? undefined}>
          {gpu.deviceSummary}
        </div>
      )}
      <NodeOverrideLine node={node} />
      {gpu.live.map((device) => (
        <div
          key={device.key}
          className="text-muted-foreground flex flex-wrap items-baseline gap-x-1.5 text-xs"
          title={device.title}
        >
          <span className="font-mono">{device.label}</span>
          <span className={cn("tabular-nums", device.busyMuted ? "" : "text-foreground")}>
            {device.busy}
          </span>
          <span>· {device.sessions}</span>
        </div>
      ))}
    </div>
  );
}

interface NodeSectionProps {
  type: NodeType;
  nodes: StreamNode[];
  /**
   * Every node of both types. Shared-GPU detection needs it: a proxy and a
   * transcode node on one host share that host's card, and each table only
   * holds half of that pair.
   */
  allNodes: StreamNode[];
  infoBanner: ReactNode;
  showJobs: boolean;
  onAdd: () => void;
  onEdit: (node: StreamNode) => void;
  onDelete: (node: StreamNode) => void;
  onToggle: (node: StreamNode) => void;
  onCheckHealth: (node: StreamNode) => void;
  checkingHealthId: number | null;
  onReprobe: (node: StreamNode) => void;
  reprobingId: number | null;
}

function NodeSection({
  type,
  nodes,
  allNodes,
  infoBanner,
  showJobs,
  onAdd,
  onEdit,
  onDelete,
  onToggle,
  onCheckHealth,
  checkingHealthId,
  onReprobe,
  reprobingId,
}: NodeSectionProps) {
  const label = type === "proxy" ? "Proxy" : "Transcode";
  const colCount = (showJobs ? 10 : 9) + (type === "proxy" ? 1 : 0);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <h2 className="text-lg font-semibold">{label} Nodes</h2>
          <Badge variant="secondary">{nodes.length}</Badge>
        </div>
        <Button size="sm" onClick={onAdd}>
          <Plus className="mr-1 h-4 w-4" /> Add {label}
        </Button>
      </div>

      {infoBanner}

      <div className="surface-panel overflow-x-auto rounded-xl border-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>URL</TableHead>
              <TableHead>Group</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Health</TableHead>
              <TableHead>GPU</TableHead>
              <TableHead>System</TableHead>
              {showJobs && <TableHead>{type === "proxy" ? "Streams" : "Jobs"}</TableHead>}
              {type === "proxy" && <TableHead>Egress</TableHead>}
              <TableHead>Last Check</TableHead>
              <TableHead className="w-40">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {nodes.length === 0 ? (
              <TableRow>
                <TableCell colSpan={colCount} className="text-muted-foreground py-8 text-center">
                  <div className="space-y-2">
                    <p>
                      {type === "proxy"
                        ? "No proxy nodes configured. Add a proxy node to enable distributed stream delivery."
                        : "No transcode nodes configured. Add a transcode node to offload video transcoding from the main server."}
                    </p>
                    <Button variant="outline" size="sm" onClick={onAdd}>
                      <Plus className="mr-1 h-4 w-4" /> Add {label}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              nodes.map((node) => {
                const isChecking = checkingHealthId === node.id;
                const isReprobing = reprobingId === node.id;
                return (
                  <TableRow key={node.id}>
                    <TableCell className="font-medium">{node.name}</TableCell>
                    <TableCell className="font-mono text-sm">{node.url}</TableCell>
                    <TableCell>
                      {node.group ? (
                        <Badge variant="outline">{node.group}</Badge>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Switch checked={node.enabled} onCheckedChange={() => onToggle(node)} />
                    </TableCell>
                    <TableCell>
                      <span className="flex items-center gap-1.5">
                        <span
                          className={`h-2.5 w-2.5 rounded-full ${
                            !node.enabled
                              ? "bg-muted-foreground"
                              : node.healthy
                                ? "bg-success"
                                : "bg-destructive"
                          }`}
                        />
                        <span className="text-muted-foreground text-sm">
                          {!node.enabled ? "Disabled" : node.healthy ? "Healthy" : "Unhealthy"}
                        </span>
                      </span>
                    </TableCell>
                    <TableCell>
                      <NodeGPUCell node={node} allNodes={allNodes} />
                    </TableCell>
                    <TableCell>
                      <NodeSystemCell node={node} />
                    </TableCell>
                    {showJobs && (
                      <TableCell>
                        {node.active_jobs}
                        {node.max_jobs != null && (
                          <span className="text-muted-foreground"> / {node.max_jobs}</span>
                        )}
                      </TableCell>
                    )}
                    {type === "proxy" && (
                      <TableCell className="text-sm whitespace-nowrap">
                        {formatMbps(node.egress_kbps)}
                        {node.max_bandwidth_kbps != null && (
                          <span className="text-muted-foreground">
                            {" "}
                            / {formatMbps(node.max_bandwidth_kbps)}
                          </span>
                        )}{" "}
                        Mbps
                      </TableCell>
                    )}
                    <TableCell className="text-muted-foreground text-xs">
                      {node.last_health_check ? formatDateTime(node.last_health_check) : "Never"}
                    </TableCell>
                    <TableCell>
                      <div className="flex gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          disabled={isChecking}
                          aria-label={`Check health of ${node.name}`}
                          onClick={() => onCheckHealth(node)}
                        >
                          <RefreshCw
                            className={`h-3 w-3 ${isChecking ? "animate-spin" : ""}`}
                            aria-hidden="true"
                          />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          disabled={isReprobing}
                          aria-label={`Re-probe hardware on ${node.name}`}
                          title="Re-verify this node's hardware against live devices. Use after a driver or device change; it can take a couple of minutes, and is refused while the node is transcoding."
                          onClick={() => onReprobe(node)}
                        >
                          <ScanSearch
                            className={`h-3 w-3 ${isReprobing ? "animate-pulse" : ""}`}
                            aria-hidden="true"
                          />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          aria-label={`Edit ${node.name}`}
                          onClick={() => onEdit(node)}
                        >
                          <Pencil className="h-3 w-3" aria-hidden="true" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          aria-label={`Delete ${node.name}`}
                          onClick={() => onDelete(node)}
                        >
                          <Trash2 className="h-3 w-3" aria-hidden="true" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

/**
 * Per-device toggles for one node's device override, mirroring the cluster-wide
 * picker on the Playback settings page — same control, same "nothing selected
 * means inherit" rule, so the two overrides read the same way.
 *
 * "Inherit" is spelled two ways on purpose: it is what an empty selection means,
 * and it is a button, because clearing several toggles one at a time to get back
 * to the default is not an obvious way to say "follow the cluster".
 */
function NodeDevicePicker({
  rows,
  onToggle,
  onInherit,
}: {
  rows: NodeHWDeviceRow[];
  onToggle: (path: string) => void;
  onInherit: () => void;
}) {
  const selectedCount = rows.filter((row) => row.selected).length;

  return (
    <div className="space-y-2">
      <div className="space-y-2">
        {rows.map((row) => (
          <div key={row.path} className="flex items-center justify-between gap-3">
            <div className="min-w-0" title={row.title}>
              <p className={cn("truncate text-sm", !row.reported && "text-muted-foreground")}>
                {row.description}
              </p>
              <p className="text-muted-foreground truncate font-mono text-xs">{row.path}</p>
            </div>
            <Switch
              checked={row.selected}
              aria-label={`Transcode on ${row.path}`}
              onCheckedChange={() => onToggle(row.path)}
            />
          </div>
        ))}
      </div>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="text-muted-foreground text-sm">
          {selectedCount === 0
            ? "Using the cluster default (auto-discover this node's devices)."
            : selectedCount === 1
              ? "All transcodes on this node run on the selected device."
              : "Transcodes on this node balance across the selected devices (least loaded first)."}
        </p>
        {selectedCount > 0 && (
          <Button type="button" variant="ghost" size="sm" className="h-7 px-2" onClick={onInherit}>
            Use cluster default
          </Button>
        )}
      </div>
    </div>
  );
}

function NodeForm({
  node,
  nodeType,
  onClose,
}: {
  node: StreamNode | null;
  nodeType: NodeType;
  onClose: () => void;
}) {
  const [name, setName] = useState(node?.name ?? "");
  const [url, setUrl] = useState(node?.url ?? "");
  const [group, setGroup] = useState(node?.group ?? "");
  const [maxJobs, setMaxJobs] = useState(node?.max_jobs?.toString() ?? "");
  const [maxBandwidthMbps, setMaxBandwidthMbps] = useState(
    node?.max_bandwidth_kbps ? (node.max_bandwidth_kbps / 1000).toString() : "",
  );
  const [hwAccelOverride, setHwAccelOverride] = useState(
    node?.hw_accel_override?.trim() || HW_ACCEL_INHERIT,
  );
  const [hwDeviceOverride, setHwDeviceOverride] = useState(node?.hw_device_override ?? "");
  // The picker is driven by the node's own reported inventory; a node that has
  // never reported one keeps the free-text field, since the override still has
  // to be settable on a node this server has not heard from yet.
  // NVENC names GPUs by CUDA index or UUID, so the render-path picker is
  // meaningless for it — the same rule the cluster-wide Playback form applies.
  const usesCUDADevices = nodeUsesCUDADevices(node, hwAccelOverride);
  const hasDeviceInventory = nodeHasHWDeviceInventory(node) && !usesCUDADevices;
  const deviceRows = buildNodeHWDeviceRows(node, hwDeviceOverride);
  const devicePaths = nodeHWDevicePaths(node);
  const effectiveAcceleration = node ? describeEffectiveAcceleration(node) : null;
  const createMutation = useCreateNode();
  const updateMutation = useUpdateNode();
  const isPending = createMutation.isPending || updateMutation.isPending;

  const urlPlaceholder =
    nodeType === "proxy" ? "https://proxy1.example.com" : "http://10.0.0.5:8082";

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    // The backend treats an empty group as "ungrouped" and caps <= 0 as
    // "unlimited", so cleared inputs reset those fields.
    const parsedMaxJobs = parseInt(maxJobs, 10);
    const parsedMaxBandwidthMbps = parseFloat(maxBandwidthMbps);
    const fields = {
      name,
      url,
      group: group.trim(),
      max_jobs: Number.isNaN(parsedMaxJobs) ? 0 : parsedMaxJobs,
      max_bandwidth_kbps: Number.isNaN(parsedMaxBandwidthMbps)
        ? 0
        : Math.round(parsedMaxBandwidthMbps * 1000),
    };
    if (node) {
      // null on either override is what restores inheritance of the
      // cluster-wide playback setting; omitting the key would leave the stored
      // value alone instead. The override controls only render for transcode
      // nodes, so a proxy edit must omit both keys rather than send null —
      // sending null here would clear an existing value the form never showed.
      const body: UpdateNodeRequest = { ...fields };
      if (nodeType === "transcode") {
        const overrideDevices = parseHWDeviceOverride(hwDeviceOverride);
        body.hw_accel_override = hwAccelOverride === HW_ACCEL_INHERIT ? null : hwAccelOverride;
        body.hw_device_override = overrideDevices.length > 0 ? overrideDevices.join(",") : null;
      }
      updateMutation.mutate({ id: node.id, body }, { onSuccess: onClose });
    } else {
      const body: CreateNodeRequest = { type: nodeType, ...fields };
      createMutation.mutate(body, { onSuccess: onClose });
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div className="space-y-2">
        <Label>Name</Label>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={nodeType === "proxy" ? "Proxy Node 1" : "Transcode Node 1"}
          required
        />
      </div>

      <div className="space-y-2">
        <Label>Type</Label>
        <Badge variant="secondary" className="text-sm">
          {nodeType === "proxy" ? "Proxy" : "Transcode"}
        </Badge>
      </div>

      <div className="space-y-2">
        <Label>URL</Label>
        <Input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder={urlPlaceholder}
          required
        />
        {nodeType === "transcode" ? (
          <p className="text-muted-foreground text-sm">
            Must be reachable from proxy nodes and the backend server. A private/internal IP or
            localhost is fine — no public URL needed.
          </p>
        ) : (
          <p className="text-muted-foreground text-sm">
            Must be publicly accessible by streaming clients.
          </p>
        )}
      </div>

      <div className="space-y-2">
        <Label>Group</Label>
        <Input value={group} onChange={(e) => setGroup(e.target.value)} placeholder="e.g. rack-1" />
        <p className="text-muted-foreground text-sm">
          Optional. Nodes in the same group are treated as co-located: transcoded streams are served
          by a proxy from the transcode node's group, keeping traffic on the same LAN. A group is
          only used while all of its nodes are healthy.
        </p>
      </div>

      <div className="space-y-2">
        <Label>{nodeType === "proxy" ? "Max Streams" : "Max Transcodes"}</Label>
        <Input
          type="number"
          min={0}
          value={maxJobs}
          onChange={(e) => setMaxJobs(e.target.value)}
          placeholder="Unlimited"
        />
        <p className="text-muted-foreground text-sm">
          Optional concurrency cap for this node. Leave empty (or 0) for unlimited.
        </p>
      </div>

      {nodeType === "proxy" && (
        <div className="space-y-2">
          <Label>Max Egress Bandwidth (Mbps)</Label>
          <Input
            type="number"
            min={0}
            step="any"
            value={maxBandwidthMbps}
            onChange={(e) => setMaxBandwidthMbps(e.target.value)}
            placeholder="Unlimited"
          />
          <p className="text-muted-foreground text-sm">
            Optional. New streams are routed elsewhere once this node's measured egress (plus the
            expected bitrate of the new stream) would exceed the cap. Active streams are never
            interrupted. Leave empty (or 0) for unlimited.
          </p>
        </div>
      )}

      {/* Overrides are edit-only: the create endpoint takes no acceleration
          fields, so offering them here would silently drop what was typed.
          They are also transcode-only: a proxy node only remuxes/strips
          bitstreams, so it never encodes and these fields would be
          meaningless — and their absence keeps a proxy edit from sending
          override fields at all. */}
      {node && nodeType === "transcode" && (
        <>
          <div className="space-y-2">
            <Label htmlFor="node-hw-accel-override">Hardware Acceleration</Label>
            <Select value={hwAccelOverride} onValueChange={setHwAccelOverride}>
              <SelectTrigger id="node-hw-accel-override" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {HW_ACCEL_OVERRIDE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-muted-foreground text-sm">
              Optional. Overrides the cluster-wide Hardware Acceleration setting for this node only
              — use it when this node's hardware differs from the rest of the cluster. The cluster
              default is Auto unless changed on the Playback settings page, and Auto detects this
              node's own hardware, not the server's. Applies to new transcodes within a minute;
              restart the node to re-prime its encoder for the new backend.
            </p>
            {effectiveAcceleration && (
              <p className="text-muted-foreground text-sm">{effectiveAcceleration}</p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor={hasDeviceInventory ? undefined : "node-hw-device-override"}>
              GPU Devices
            </Label>
            {hasDeviceInventory ? (
              <NodeDevicePicker
                rows={deviceRows}
                onToggle={(path) =>
                  setHwDeviceOverride(toggleHWDevice(hwDeviceOverride, path, devicePaths))
                }
                onInherit={() => setHwDeviceOverride("")}
              />
            ) : (
              <>
                <Input
                  id="node-hw-device-override"
                  value={hwDeviceOverride}
                  onChange={(e) => setHwDeviceOverride(e.target.value)}
                  placeholder={
                    usesCUDADevices
                      ? "Cluster default (CUDA device 0)"
                      : "Cluster default (auto-discover)"
                  }
                />
                <p className="text-muted-foreground text-sm">
                  {usesCUDADevices ? (
                    <>
                      Optional. The CUDA device this node encodes on — an index or a GPU UUID (e.g.{" "}
                      <span className="font-mono">0</span> or{" "}
                      <span className="font-mono">GPU-a1b2c3d4</span>). NVENC does not use{" "}
                      <span className="font-mono">/dev/dri</span> render paths. Leave
                    </>
                  ) : (
                    <>
                      Optional. Comma-separated render device paths this node transcodes on (e.g.{" "}
                      <span className="font-mono">/dev/dri/renderD128,/dev/dri/renderD129</span>).
                      Leave
                    </>
                  )}
                  empty to use the cluster default (auto-discover). This node has reported no device
                  inventory yet, so there is nothing to pick from.
                </p>
              </>
            )}
          </div>
        </>
      )}

      <Button type="submit" className="w-full" disabled={isPending}>
        {isPending ? "Saving..." : "Save"}
      </Button>
    </form>
  );
}

export default function AdminNodes() {
  const { data: nodes = [], isLoading } = useAdminNodes();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingNode, setEditingNode] = useState<StreamNode | null>(null);
  const [addingNodeType, setAddingNodeType] = useState<NodeType | null>(null);
  const [confirmDeleteNode, setConfirmDeleteNode] = useState<StreamNode | null>(null);
  const deleteMutation = useDeleteNode();
  const checkHealthMutation = useCheckNodeHealth();
  const reprobeMutation = useReprobeNode();
  const toggleMutation = useToggleNode();

  const proxyNodes = nodes.filter((n) => n.type === "proxy");
  const transcodeNodes = nodes.filter((n) => n.type === "transcode");

  const checkingHealthId =
    checkHealthMutation.isPending && checkHealthMutation.variables
      ? checkHealthMutation.variables.id
      : null;

  const reprobingId =
    reprobeMutation.isPending && reprobeMutation.variables ? reprobeMutation.variables.id : null;

  const resolvedNodeType: NodeType = editingNode
    ? (editingNode.type as NodeType)
    : (addingNodeType ?? "proxy");

  function handleAdd(type: NodeType) {
    setAddingNodeType(type);
    setEditingNode(null);
    setDialogOpen(true);
  }

  function handleEdit(node: StreamNode) {
    setEditingNode(node);
    setAddingNodeType(null);
    setDialogOpen(true);
  }

  function handleDelete(node: StreamNode) {
    setConfirmDeleteNode(node);
  }

  function handleDialogChange(open: boolean) {
    setDialogOpen(open);
    if (!open) {
      setEditingNode(null);
      setAddingNodeType(null);
    }
  }

  if (isLoading) return <div className="page-shell py-8">Loading nodes...</div>;

  return (
    <div className="page-shell space-y-8 py-4 sm:py-6">
      <div className="page-header gap-5">
        <div className="space-y-3">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Stream Nodes</h1>
          <p className="page-subtitle text-sm sm:text-base">
            Manage proxy and transcode workers that distribute playback load across your
            infrastructure.
          </p>
        </div>
      </div>

      <ConfirmDialog
        open={confirmDeleteNode !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmDeleteNode(null);
        }}
        title="Delete node"
        description={`Delete stream node "${confirmDeleteNode?.name}"? This action cannot be undone.`}
        confirmLabel="Delete"
        variant="destructive"
        onConfirm={() => {
          if (confirmDeleteNode) deleteMutation.mutate(confirmDeleteNode.id);
          setConfirmDeleteNode(null);
        }}
      />

      <NodeSection
        type="proxy"
        nodes={proxyNodes}
        allNodes={nodes}
        showJobs={true}
        onAdd={() => handleAdd("proxy")}
        onEdit={handleEdit}
        onDelete={handleDelete}
        onToggle={(node) => toggleMutation.mutate(node)}
        onCheckHealth={(node) => checkHealthMutation.mutate(node)}
        checkingHealthId={checkingHealthId}
        onReprobe={(node) => reprobeMutation.mutate(node)}
        reprobingId={reprobingId}
        infoBanner={
          <div className="surface-panel-subtle text-info flex items-start gap-2 rounded-xl p-3 text-sm">
            <Info className="mt-0.5 h-4 w-4 shrink-0" />
            <p>Proxy nodes relay streams to end users. The URL must be publicly accessible.</p>
          </div>
        }
      />

      <NodeSection
        type="transcode"
        nodes={transcodeNodes}
        allNodes={nodes}
        showJobs={true}
        onAdd={() => handleAdd("transcode")}
        onEdit={handleEdit}
        onDelete={handleDelete}
        onToggle={(node) => toggleMutation.mutate(node)}
        onCheckHealth={(node) => checkHealthMutation.mutate(node)}
        checkingHealthId={checkingHealthId}
        onReprobe={(node) => reprobeMutation.mutate(node)}
        reprobingId={reprobingId}
        infoBanner={
          <div className="surface-panel-subtle text-warning flex items-start gap-2 rounded-xl p-3 text-sm">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <p>
              Transcode nodes handle video transcoding internally.{" "}
              <strong>Must be on the same network as proxy nodes and the backend.</strong> Does not
              need a public URL.
            </p>
          </div>
        }
      />

      <Dialog open={dialogOpen} onOpenChange={handleDialogChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingNode
                ? "Edit Node"
                : `Add ${resolvedNodeType === "proxy" ? "Proxy" : "Transcode"} Node`}
            </DialogTitle>
          </DialogHeader>
          <NodeForm
            node={editingNode}
            nodeType={resolvedNodeType}
            onClose={() => handleDialogChange(false)}
          />
        </DialogContent>
      </Dialog>
    </div>
  );
}
