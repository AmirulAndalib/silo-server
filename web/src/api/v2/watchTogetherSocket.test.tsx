import { createContext, useContext, useState, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, renderHook, screen, waitFor } from "@testing-library/react";
import {
  captureProfileRequestContext,
  fetchWithSession,
  setRefreshToken,
  setAccessToken,
  setProfileId,
  setProfileToken,
} from "@/api/client";
import { mintRoomSocketTicket } from "./watchTogetherSocket";
import { useWatchTogetherRoomConnection } from "@/player/hooks/useWatchTogetherRoomConnection";
import { getWatchTogetherRoom, type WatchTogetherRoomSnapshot } from "@/lib/watchTogether";
const AuthUpdates = createContext(0);
vi.mock("@/hooks/useAuth", () => ({ useOptionalAuth: () => useContext(AuthUpdates) }));
vi.mock("@/lib/watchTogether", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/watchTogether")>()),
  getWatchTogetherRoom: vi.fn(async (roomId: string) => ({
    room: { room_id: roomId, generation: 1 },
    room_access_token: "room-proof",
  })),
  listWatchTogetherSuggestions: vi.fn(async () => ({ suggestions: [] })),
}));
class RoomSocket extends EventTarget {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 3;
  static all: RoomSocket[] = [];
  readyState = 0;
  protocol = "silo.room.v2";
  send = vi.fn();
  constructor(
    readonly url: string,
    readonly protocols: string[],
  ) {
    super();
    RoomSocket.all.push(this);
  }
  open() {
    this.readyState = 1;
    this.dispatchEvent(new Event("open"));
  }
  close() {
    this.readyState = 3;
    this.dispatchEvent(new Event("close"));
  }
  message(data: unknown) {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(data) }));
  }
}
const ticket = (letter = "a") =>
  new Response(
    JSON.stringify({
      ticket: letter.repeat(43),
      protocol: "silo.room.v2",
      expires_in: 29,
      max_connection_seconds: 300,
    }),
    { headers: { "Content-Type": "application/json" } },
  );
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  setAccessToken("login");
  setProfileId("profile");
  setProfileToken("pin-A");
  RoomSocket.all = [];
  vi.stubGlobal("WebSocket", RoomSocket);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("captures original proof and authority without authentication replay", async () => {
  const fetch = vi.fn().mockImplementation(async () => ticket());
  vi.stubGlobal("fetch", fetch);
  await mintRoomSocketTicket("room", "original-proof");
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0]![0]).toBe("/api/v2/watch-together/rooms/room/ws-ticket");
  const headers = new Headers(fetch.mock.calls[0]![1].headers);
  expect(headers.get("X-Room-Token")).toBe("original-proof");
  expect(headers.get("X-Profile-Token")).toBe("pin-A");
});
it.each([401, 403, 409, 422, 500])("single sends ticket refusal %s", async (status) => {
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status }));
  vi.stubGlobal("fetch", fetch);
  await expect(mintRoomSocketTicket("room", "proof")).rejects.toThrow();
  expect(fetch).toHaveBeenCalledTimes(1);
});
it("refuses replaced authority before dispatch", async () => {
  const original = captureProfileRequestContext();
  setProfileToken("pin-B");
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  await expect(mintRoomSocketTicket("room", "proof", original)).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
});
it("mounted socket uses no URL credentials and rebinds replaced same-profile PIN", async () => {
  const fetch = vi.fn().mockImplementation(async () => ticket());
  vi.stubGlobal("fetch", fetch);
  const view = renderHook(() =>
    useWatchTogetherRoomConnection({ roomId: "room", roomToken: "room-proof" }),
  );
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  const old = RoomSocket.all[0]!;
  expect(new URL(old.url).pathname).toBe("/api/v2/watch-together/rooms/room/ws");
  expect(new URL(old.url).search).toBe("");
  expect(old.protocols).toEqual(["silo.room.v2", `silo.ticket.${"a".repeat(43)}`]);
  act(() => old.open());
  expect(view.result.current.connectionState).toBe("connected");
  act(() => old.message({ type: "snapshot", room: { room_id: "room", generation: 20 } }));
  expect(view.result.current.room?.generation).toBe(20);
  setProfileToken("pin-B");
  view.rerender();
  await waitFor(() => expect(RoomSocket.all.length).toBe(2));
  expect(old.readyState).toBe(RoomSocket.CLOSED);
  act(() => old.message({ type: "snapshot", room: { room_id: "room", generation: 99 } }));
  expect(view.result.current.room?.generation).not.toBe(99);
  act(() => old.close());
  expect(RoomSocket.all.length).toBe(2);
  const current = RoomSocket.all[1]!;
  act(() => current.open());
  const headers = new Headers(fetch.mock.calls[1]![1].headers);
  expect(headers.get("X-Profile-Token")).toBe("pin-B");
  act(() => {
    expect(view.result.current.sendRoomMessage({ type: "ready", session_id: "session" }).ok).toBe(
      true,
    );
  });
  setProfileToken("pin-C");
  expect(view.result.current.sendRoomMessage({ type: "ready", session_id: "session" }).ok).toBe(
    false,
  );
});
it.each([201, 500])(
  "ignores old pending ticket after authority replacement (%s)",
  async (status) => {
    const pending: Array<(r: Response) => void> = [];
    const fetch = vi.fn().mockImplementation(() => new Promise<Response>((r) => pending.push(r)));
    vi.stubGlobal("fetch", fetch);
    const view = renderHook(() =>
      useWatchTogetherRoomConnection({ roomId: "room", roomToken: "room-proof" }),
    );
    await waitFor(() => expect(pending.length).toBe(1));
    setProfileToken("pin-B");
    view.rerender();
    await waitFor(() => expect(pending.length).toBe(2));
    await act(async () =>
      pending[0]!(status === 201 ? ticket("a") : new Response(null, { status })),
    );
    expect(RoomSocket.all.length).toBe(0);
    expect(view.result.current.closedReason).toBeNull();
    await act(async () => pending[1]!(ticket("b")));
    expect(RoomSocket.all.length).toBe(1);
    expect(RoomSocket.all[0]!.protocols[1]).toBe(`silo.ticket.${"b".repeat(43)}`);
  },
);
it("gets a fresh ticket after socket close and preserves ping/message callbacks", async () => {
  let count = 0;
  const fetch = vi.fn().mockImplementation(async () => ticket(++count === 1 ? "a" : "b"));
  vi.stubGlobal("fetch", fetch);
  const view = renderHook(() =>
    useWatchTogetherRoomConnection({ roomId: "room", roomToken: "room-proof" }),
  );
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  const old = RoomSocket.all[0]!;
  act(() => old.open());
  expect(JSON.parse(old.send.mock.calls[0]![0]).type).toBe("ping");
  act(() => old.message({ type: "transport_command", command: { action: "play" } }));
  expect(view.result.current.transportCommand?.action).toBe("play");
  act(() => old.close());
  await waitFor(() => expect(RoomSocket.all.length).toBe(2));
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(RoomSocket.all[1]!.protocols[1]).toBe(`silo.ticket.${"b".repeat(43)}`);
});
it("terminal ticket refusal opens no socket and does not reconnect", async () => {
  const fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        type: "https://siloserver.org/docs/api/v2/problems/permission_denied",
        title: "Permission denied",
        status: 403,
      }),
      { status: 403, headers: { "Content-Type": "application/problem+json" } },
    ),
  );
  vi.stubGlobal("fetch", fetch);
  const view = renderHook(() =>
    useWatchTogetherRoomConnection({ roomId: "room", roomToken: "room-proof" }),
  );
  await waitFor(() => expect(view.result.current.closedReason).toBe("forbidden"));
  expect(RoomSocket.all.length).toBe(0);
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("auth provider update reconnects without changing room props or rerendering the hook explicitly", async () => {
  function Provider({ children }: { children: ReactNode }) {
    const [revision, setRevision] = useState(0);
    return (
      <AuthUpdates.Provider value={revision}>
        <button
          onClick={() => {
            setProfileToken("provider-pin-B");
            setRevision((value) => value + 1);
          }}
        >
          Replace authority
        </button>
        {children}
      </AuthUpdates.Provider>
    );
  }
  const fetch = vi.fn().mockImplementation(async () => ticket());
  vi.stubGlobal("fetch", fetch);
  renderHook(() => useWatchTogetherRoomConnection({ roomId: "room", roomToken: "room-proof" }), {
    wrapper: Provider,
  });
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  const old = RoomSocket.all[0]!;
  act(() => old.open());
  fireEvent.click(screen.getByRole("button", { name: "Replace authority" }));
  await waitFor(() => expect(RoomSocket.all.length).toBe(2));
  expect(old.readyState).toBe(RoomSocket.CLOSED);
  expect(new Headers(fetch.mock.calls[1]![1].headers).get("X-Profile-Token")).toBe(
    "provider-pin-B",
  );
});

it("fresh reconnect delegates an already-rotated access token under the same captured authority", async () => {
  const original = captureProfileRequestContext();
  setRefreshToken("synthetic-refresh");
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(new Response(null, { status: 401 }))
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({ access_token: "rotated-login", refresh_token: "next-refresh" }),
        { headers: { "Content-Type": "application/json" } },
      ),
    )
    .mockResolvedValueOnce(new Response(null, { status: 200 }))
    .mockResolvedValueOnce(ticket());
  vi.stubGlobal("fetch", fetch);
  await fetchWithSession("/synthetic-existing-read", {});
  await mintRoomSocketTicket("room", "original-room-proof", original);
  expect(fetch).toHaveBeenCalledTimes(4);
  expect(fetch.mock.calls[3]![0]).toBe("/api/v2/watch-together/rooms/room/ws-ticket");
  const headers = new Headers(fetch.mock.calls[3]![1].headers);
  expect(headers.get("Authorization")).toBe("Bearer rotated-login");
  expect(headers.get("X-Room-Token")).toBe("original-room-proof");
  expect(headers.get("X-Profile-Token")).toBe("pin-A");
});

it("keeps the fallback receipt when an older socket snapshot arrives", async () => {
  const replacement = {
    room_id: "room",
    generation: 10,
    selection_revision: 2,
    selected_file_id: "8",
    selected_content_id: "movie",
    phase: "playing",
    playback_state: "waiting",
    selection_mode: "host_pick",
    guest_control_policy: "host_only",
    self_role: "guest",
    anchor_updated_at: "2026-01-01T00:00:00Z",
  };
  const fetch = vi.fn(async (url: string) =>
    url.endsWith("/source-fallback")
      ? new Response(JSON.stringify({ room: replacement, room_access_token: "proof" }), {
          headers: { "Content-Type": "application/json" },
        })
      : ticket(),
  );
  vi.stubGlobal("fetch", fetch);
  const view = renderHook(() =>
    useWatchTogetherRoomConnection({ roomId: "room", roomToken: "proof" }),
  );
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  const socket = RoomSocket.all[0]!;
  act(() => socket.open());
  const report = { selectionRevision: 1, failedFileId: 7, reason: "no_alternate_version" as const };
  await act(async () => {
    await view.result.current.fallbackSource(report);
  });
  expect(view.result.current.room?.selected_file_id).toBe(8);
  act(() =>
    socket.message({
      type: "snapshot",
      room: { ...replacement, selected_file_id: 7, generation: 9, selection_revision: 1 },
    }),
  );
  expect(view.result.current.room?.generation).toBe(10);
  expect(view.result.current.room?.selected_file_id).toBe(8);
  const oldFallback = view.result.current.fallbackSource;
  const calls = fetch.mock.calls.length;
  setProfileToken("pin-B");
  await expect(oldFallback(report)).rejects.toThrow();
  expect(fetch).toHaveBeenCalledTimes(calls);
});

it("fences source fallback callbacks and receipts when the room proof changes", async () => {
  let finish!: (response: Response) => void;
  const fetch = vi.fn((url: string) =>
    url.endsWith("/source-fallback")
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : Promise.resolve(ticket()),
  );
  vi.stubGlobal("fetch", fetch);
  const view = renderHook(
    ({ proof }) => useWatchTogetherRoomConnection({ roomId: "room", roomToken: proof }),
    { initialProps: { proof: "original-proof" } },
  );
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  act(() => RoomSocket.all[0]!.open());
  const original = view.result.current.fallbackSource;
  const report = { selectionRevision: 1, failedFileId: 7, reason: "no_alternate_version" as const };
  let pending!: ReturnType<typeof original>;
  act(() => {
    pending = original(report);
  });
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  view.rerender({ proof: "renewed-proof" });
  await act(async () => {
    finish(
      new Response(
        JSON.stringify({
          room: {
            room_id: "room",
            generation: 10,
            selection_revision: 2,
            selected_file_id: "8",
            selected_content_id: "movie",
            phase: "playing",
            playback_state: "waiting",
            selection_mode: "host_pick",
            guest_control_policy: "host_only",
            self_role: "guest",
            anchor_updated_at: "2026-01-01T00:00:00Z",
          },
          room_access_token: "original-proof",
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
    );
    expect(await pending).toBeNull();
  });
  expect(view.result.current.room?.generation).not.toBe(10);
  expect(await original(report)).toBeNull();
  expect(fetch.mock.calls.filter(([url]) => url.endsWith("/source-fallback"))).toHaveLength(1);
  const latest = view.result.current.fallbackSource;
  view.unmount();
  expect(await latest(report)).toBeNull();
  expect(fetch.mock.calls.filter(([url]) => url.endsWith("/source-fallback"))).toHaveLength(1);
});

it.each([1, 2, 3])(
  "merges delayed HTTP generation %s after a socket attachment",
  async (generation) => {
    let finish!: (response: Awaited<ReturnType<typeof getWatchTogetherRoom>>) => void;
    vi.mocked(getWatchTogetherRoom).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ticket()),
    );
    const view = renderHook(() =>
      useWatchTogetherRoomConnection({ roomId: "room", roomToken: "proof" }),
    );
    await waitFor(() => expect(RoomSocket.all.length).toBe(1));
    const socket = RoomSocket.all[0]!;
    const attached = {
      room_id: "room",
      generation: 2,
      attached_session_id: "session",
      member_count: 1,
      members: [{ user_id: 1, profile_id: "profile", connected: true, is_ready: true }],
    } as WatchTogetherRoomSnapshot;
    act(() => {
      socket.open();
      socket.message({ type: "snapshot", room: attached });
    });
    const delayed = {
      ...attached,
      generation,
      attached_session_id: undefined,
      members: [],
      member_count: 0,
    };
    await act(async () => finish({ room: delayed, room_access_token: "proof" }));
    expect(view.result.current.room).toEqual(generation > 2 ? delayed : attached);
  },
);

it("preserves an attachment received while a source fallback receipt is pending", async () => {
  let finish!: (response: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      url.endsWith("/source-fallback")
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(ticket()),
    ),
  );
  const view = renderHook(() =>
    useWatchTogetherRoomConnection({ roomId: "room", roomToken: "proof" }),
  );
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  const socket = RoomSocket.all[0]!;
  act(() => socket.open());
  let pending!: ReturnType<typeof view.result.current.fallbackSource>;
  act(() => {
    pending = view.result.current.fallbackSource({
      selectionRevision: 1,
      failedFileId: 7,
      reason: "no_alternate_version",
    });
  });
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  const receipt = {
    room_id: "room",
    generation: 2,
    selection_revision: 2,
    selected_file_id: "8",
    selected_content_id: "movie",
    phase: "playing",
    playback_state: "waiting",
    selection_mode: "host_pick",
    guest_control_policy: "host_only",
    self_role: "guest",
    anchor_updated_at: "2026-01-01T00:00:00Z",
  };
  act(() =>
    socket.message({
      type: "snapshot",
      room: {
        ...receipt,
        selected_file_id: 8,
        attached_session_id: "replacement-session",
      },
    }),
  );
  await act(async () => {
    finish(
      new Response(JSON.stringify({ room: receipt, room_access_token: "proof" }), {
        headers: { "Content-Type": "application/json" },
      }),
    );
    await pending;
  });
  expect(view.result.current.room?.attached_session_id).toBe("replacement-session");
});

it("accepts an equal-generation HTTP read started after the last socket snapshot", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ticket()),
  );
  const view = renderHook(
    ({ proof }) => useWatchTogetherRoomConnection({ roomId: "room", roomToken: proof }),
    { initialProps: { proof: "old-proof" } },
  );
  await waitFor(() => expect(RoomSocket.all.length).toBe(1));
  act(() => {
    RoomSocket.all[0]!.open();
    RoomSocket.all[0]!.message({ type: "snapshot", room: { room_id: "room", generation: 1 } });
  });
  vi.mocked(getWatchTogetherRoom).mockResolvedValueOnce({
    room: {
      room_id: "room",
      generation: 1,
      attached_session_id: "fresh-session",
    } as WatchTogetherRoomSnapshot,
    room_access_token: "new-proof",
  });
  view.rerender({ proof: "new-proof" });
  await waitFor(() => expect(view.result.current.room?.attached_session_id).toBe("fresh-session"));
});
