import type {
  CSSProperties,
  DragEvent,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from "react";
import { useCallback, useRef, useState } from "react";
import { GripVertical, Plus, X } from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { getDashboardWidget } from "./registry";
import type { WidgetId } from "./types";
import type { DashboardLayout } from "./useDashboardLayout";

/** Must match the `gap` of `.admin-widget-grid` in app.css (0.875rem). */
const GRID_GAP_PX = 14;
const GRID_COLUMNS = 12;

interface DropIndicator {
  id: WidgetId;
  edge: "before" | "after";
}

interface ResizePreview {
  id: WidgetId;
  span: number;
}

export function DashboardGrid({
  layout,
  isAddPanelOpen,
  onAddPanelOpenChange,
}: {
  layout: DashboardLayout;
  isAddPanelOpen: boolean;
  onAddPanelOpenChange: (open: boolean) => void;
}) {
  const {
    entries,
    hiddenWidgets,
    isCustomizing,
    moveWidget,
    resizeWidget,
    removeWidget,
    addWidget,
  } = layout;

  const gridRef = useRef<HTMLDivElement | null>(null);
  const [draggedId, setDraggedId] = useState<WidgetId | null>(null);
  const [dropIndicator, setDropIndicator] = useState<DropIndicator | null>(null);
  const [resizePreview, setResizePreview] = useState<ResizePreview | null>(null);
  const [liveMessage, setLiveMessage] = useState("");
  const resizeSessionRef = useRef<{
    id: WidgetId;
    startX: number;
    startSpan: number;
    unit: number;
    minSpan: number;
    maxSpan: number;
    latestSpan: number;
  } | null>(null);

  const findWidgetIdFromEvent = useCallback((event: DragEvent<HTMLElement>): WidgetId | null => {
    const target = event.target as HTMLElement | null;
    const host = target?.closest<HTMLElement>("[data-widget-id]");
    return (host?.dataset.widgetId as WidgetId | undefined) ?? null;
  }, []);

  const handleDragStart = useCallback(
    (event: DragEvent<HTMLElement>) => {
      if (!isCustomizing) return;
      const id = findWidgetIdFromEvent(event);
      if (!id) return;
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", id);
      setDraggedId(id);
    },
    [findWidgetIdFromEvent, isCustomizing],
  );

  const handleDragEnd = useCallback(() => {
    setDraggedId(null);
    setDropIndicator(null);
  }, []);

  const handleDragOver = useCallback(
    (event: DragEvent<HTMLElement>) => {
      if (!draggedId) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      const overId = findWidgetIdFromEvent(event);
      if (!overId || overId === draggedId) {
        setDropIndicator(null);
        return;
      }
      const host = (event.target as HTMLElement).closest<HTMLElement>("[data-widget-id]");
      if (!host) {
        setDropIndicator(null);
        return;
      }
      const rect = host.getBoundingClientRect();
      const edge = event.clientX < rect.left + rect.width / 2 ? "before" : "after";
      setDropIndicator((prev) =>
        prev?.id === overId && prev.edge === edge ? prev : { id: overId, edge },
      );
    },
    [draggedId, findWidgetIdFromEvent],
  );

  const handleDrop = useCallback(
    (event: DragEvent<HTMLElement>) => {
      if (!draggedId) return;
      event.preventDefault();
      const overId = findWidgetIdFromEvent(event);
      if (overId && overId !== draggedId) {
        const host = (event.target as HTMLElement).closest<HTMLElement>("[data-widget-id]");
        const rect = host?.getBoundingClientRect();
        const before = rect ? event.clientX < rect.left + rect.width / 2 : true;
        if (before) {
          moveWidget(draggedId, overId);
        } else {
          const overIndex = entries.findIndex((entry) => entry.id === overId);
          const nextEntry = overIndex === -1 ? undefined : entries[overIndex + 1];
          moveWidget(draggedId, nextEntry ? nextEntry.id : null);
        }
      }
      setDraggedId(null);
      setDropIndicator(null);
    },
    [draggedId, entries, findWidgetIdFromEvent, moveWidget],
  );

  const handleResizePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLButtonElement>, id: WidgetId, currentSpan: number) => {
      if (!isCustomizing) return;
      // Only start on a primary-button press: a right-click opens the context
      // menu and never delivers the matching pointerup, which would leave the
      // resize session stuck.
      if (!event.isPrimary || event.button !== 0) return;
      const widget = getDashboardWidget(id);
      event.preventDefault();
      event.stopPropagation();
      event.currentTarget.setPointerCapture(event.pointerId);
      const gridWidth = gridRef.current?.getBoundingClientRect().width ?? 0;
      const unit = gridWidth > 0 ? (gridWidth + GRID_GAP_PX) / GRID_COLUMNS : 1;
      resizeSessionRef.current = {
        id,
        startX: event.clientX,
        startSpan: currentSpan,
        unit,
        minSpan: widget.minSpan,
        maxSpan: widget.maxSpan,
        latestSpan: currentSpan,
      };
      setResizePreview({ id, span: currentSpan });
    },
    [isCustomizing],
  );

  const handleResizePointerMove = useCallback((event: ReactPointerEvent<HTMLButtonElement>) => {
    const session = resizeSessionRef.current;
    if (!session) return;
    const raw = session.startSpan + (event.clientX - session.startX) / session.unit;
    const next = Math.min(session.maxSpan, Math.max(session.minSpan, Math.round(raw)));
    if (next !== session.latestSpan) {
      session.latestSpan = next;
      setResizePreview({ id: session.id, span: next });
    }
  }, []);

  const handleResizePointerEnd = useCallback(
    (event: ReactPointerEvent<HTMLButtonElement>) => {
      const session = resizeSessionRef.current;
      if (!session) return;
      resizeSessionRef.current = null;
      setResizePreview(null);
      if (event.currentTarget.hasPointerCapture(event.pointerId)) {
        event.currentTarget.releasePointerCapture(event.pointerId);
      }
      resizeWidget(session.id, session.latestSpan);
    },
    [resizeWidget],
  );

  const handleMoveKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLButtonElement>, id: WidgetId) => {
      const backward = event.key === "ArrowLeft" || event.key === "ArrowUp";
      const forward = event.key === "ArrowRight" || event.key === "ArrowDown";
      if (!backward && !forward) return;
      event.preventDefault();
      const index = entries.findIndex((entry) => entry.id === id);
      if (index === -1) return;
      const nextIndex = backward ? index - 1 : index + 1;
      const neighbor = entries[nextIndex];
      if (!neighbor) return;
      if (backward) {
        moveWidget(id, neighbor.id);
      } else {
        const after = entries[index + 2];
        moveWidget(id, after ? after.id : null);
      }
      setLiveMessage(
        `${getDashboardWidget(id).title} moved to position ${nextIndex + 1} of ${entries.length}`,
      );
    },
    [entries, moveWidget],
  );

  const handleResizeKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLButtonElement>, id: WidgetId, currentSpan: number) => {
      const shrink = event.key === "ArrowLeft" || event.key === "ArrowDown";
      const grow = event.key === "ArrowRight" || event.key === "ArrowUp";
      if (!shrink && !grow) return;
      event.preventDefault();
      const widget = getDashboardWidget(id);
      const next = Math.min(
        widget.maxSpan,
        Math.max(widget.minSpan, currentSpan + (grow ? 1 : -1)),
      );
      if (next === currentSpan) return;
      resizeWidget(id, next);
      setLiveMessage(`${widget.title} resized to ${next} of ${GRID_COLUMNS} columns`);
    },
    [resizeWidget],
  );

  const isResizing = resizePreview !== null;

  return (
    <>
      <span aria-live="polite" role="status" className="sr-only">
        {liveMessage}
      </span>
      <div
        ref={gridRef}
        className="admin-widget-grid"
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        {entries.map((entry) => {
          const widget = getDashboardWidget(entry.id);
          const span = resizePreview?.id === entry.id ? resizePreview.span : entry.span;
          const canResize = widget.minSpan !== widget.maxSpan;
          const isWidgetResizing = resizePreview?.id === entry.id;
          const WidgetComponent = widget.Component;

          return (
            <div
              key={entry.id}
              data-widget-id={entry.id}
              className={cn(
                "admin-widget",
                span >= 6 && "admin-widget-wide",
                isCustomizing && "rounded-2xl",
                draggedId === entry.id && "opacity-40",
              )}
              style={{ "--widget-span": span } as CSSProperties}
              draggable={isCustomizing && !isResizing}
            >
              <WidgetComponent />

              {isCustomizing && (
                <>
                  <div
                    aria-hidden="true"
                    className={cn(
                      "border-primary/40 pointer-events-none absolute -inset-1 z-10 rounded-2xl border-2 border-dashed",
                      isWidgetResizing && "border-primary/80 border-solid",
                    )}
                  />

                  {dropIndicator?.id === entry.id && (
                    <div
                      aria-hidden="true"
                      className={cn(
                        "bg-primary pointer-events-none absolute inset-y-0 z-30 w-1 rounded-full",
                        dropIndicator.edge === "before" ? "-left-2.5" : "-right-2.5",
                      )}
                    />
                  )}

                  <div className="border-border bg-background/95 absolute -top-3 right-3 z-20 flex items-center gap-0.5 rounded-full border px-1 py-0.5 shadow-md backdrop-blur">
                    <button
                      type="button"
                      aria-label={`Move ${widget.title} (drag, or arrow keys)`}
                      title="Drag or use arrow keys to move"
                      className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex h-6 w-6 cursor-grab items-center justify-center rounded-full focus-visible:ring-2 focus-visible:outline-none active:cursor-grabbing"
                      onKeyDown={(event) => handleMoveKeyDown(event, entry.id)}
                    >
                      <GripVertical className="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      aria-label={`Remove ${widget.title}`}
                      title="Remove"
                      className="text-muted-foreground hover:text-destructive focus-visible:ring-ring flex h-6 w-6 cursor-pointer items-center justify-center rounded-full focus-visible:ring-2 focus-visible:outline-none"
                      onClick={() => removeWidget(entry.id)}
                    >
                      <X className="h-3.5 w-3.5" />
                    </button>
                  </div>

                  {canResize && (
                    <button
                      type="button"
                      aria-label={`Resize ${widget.title} (drag, or arrow keys)`}
                      title="Drag or use arrow keys to resize"
                      className="border-border bg-background/95 hover:border-primary hover:bg-primary/30 focus-visible:ring-ring absolute top-1/2 -right-2 z-20 hidden h-10 w-2.5 -translate-y-1/2 cursor-ew-resize touch-none rounded-full border shadow-md focus-visible:ring-2 focus-visible:outline-none lg:block"
                      onPointerDown={(event) =>
                        handleResizePointerDown(event, entry.id, entry.span)
                      }
                      onPointerMove={handleResizePointerMove}
                      onPointerUp={handleResizePointerEnd}
                      onPointerCancel={handleResizePointerEnd}
                      onLostPointerCapture={handleResizePointerEnd}
                      onKeyDown={(event) => handleResizeKeyDown(event, entry.id, entry.span)}
                    />
                  )}

                  {isWidgetResizing && (
                    <span
                      aria-hidden="true"
                      className="bg-primary text-primary-foreground absolute -top-3 left-1/2 z-30 -translate-x-1/2 rounded-full px-2 py-0.5 text-[10px] font-bold whitespace-nowrap tabular-nums shadow-md"
                    >
                      {span} / {GRID_COLUMNS}
                    </span>
                  )}
                </>
              )}
            </div>
          );
        })}
      </div>

      <Sheet open={isAddPanelOpen} onOpenChange={onAddPanelOpenChange} modal={false}>
        <SheetContent
          side="right"
          className="w-80 gap-2 sm:max-w-sm"
          onInteractOutside={(event) => event.preventDefault()}
        >
          <SheetHeader className="pb-0">
            <SheetTitle>Add widget</SheetTitle>
            <SheetDescription>
              Widgets you&apos;ve removed or haven&apos;t placed yet.
            </SheetDescription>
          </SheetHeader>
          <div className="flex flex-col gap-2 overflow-y-auto p-4 pt-2">
            {hiddenWidgets.length === 0 ? (
              <p className="text-muted-foreground py-4 text-sm">
                Everything is on the dashboard already.
              </p>
            ) : (
              hiddenWidgets.map((widget) => (
                <button
                  key={widget.id}
                  type="button"
                  className="border-border hover:border-primary/50 hover:bg-accent/40 focus-visible:ring-ring flex cursor-pointer items-start gap-3 rounded-xl border p-3 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none"
                  onClick={() => addWidget(widget.id)}
                >
                  <span className="bg-primary/10 text-primary flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md">
                    <Plus className="h-3.5 w-3.5" />
                  </span>
                  <span className="min-w-0">
                    <span className="block text-sm font-semibold">{widget.title}</span>
                    <span className="text-muted-foreground block text-xs">
                      {widget.description}
                    </span>
                  </span>
                </button>
              ))
            )}
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
