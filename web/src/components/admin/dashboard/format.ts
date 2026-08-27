import type { ScanRun } from "@/api/types";
import { formatActiveScanMode, formatActiveScanProgress } from "@/lib/scanRuns";

export function formatFileCount(count: number | null | undefined) {
  if (count == null) {
    return "—";
  }
  return count === 1 ? "1 file" : `${count.toLocaleString()} files`;
}

export function formatDashboardLibraryScanProgress(scan: ScanRun, activeScanCount: number) {
  const status = scan.status === "running" ? "Scanning" : "Queued";
  const progress = formatActiveScanProgress(scan);
  const detail =
    progress || (scan.status === "running" ? formatActiveScanMode(scan) : "Waiting for capacity");
  const extraScans = activeScanCount > 1 ? ` + ${activeScanCount - 1} more` : "";
  return `${status}: ${detail}${extraScans}`;
}
