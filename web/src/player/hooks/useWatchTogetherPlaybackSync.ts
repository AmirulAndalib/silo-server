import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  type MutableRefObject,
  type RefObject,
} from "react";
import { toMediaTime } from "../utils/mediaTimeline";
import type { WatchTogetherRoomConnectionResult } from "./useWatchTogetherRoomConnection";

interface UseWatchTogetherPlaybackSyncOptions {
  roomConnection: WatchTogetherRoomConnectionResult;
  sessionId?: string | null;
  videoRef: RefObject<HTMLVideoElement | null>;
  streamOriginRef: MutableRefObject<number>;
  appliedCommandIdRef: RefObject<string | null>;
}

interface TransportRequestResult {
  ok: boolean;
}

interface UseWatchTogetherPlaybackSyncResult {
  attachedSessionId: string | null;
  requestTransport: (
    action: "play" | "pause" | "seek",
    positionSeconds: number,
    isPaused: boolean,
  ) => TransportRequestResult;
  reportReady: () => TransportRequestResult;
  reportBuffering: (positionSeconds?: number, isPaused?: boolean) => TransportRequestResult;
}

const stateReportIntervalMs = 1_500;
const pendingCommandQuietPeriodMs = 250;
const readySeekToleranceSeconds = 1;
const bufferingGraceMs = 500;

export function useWatchTogetherPlaybackSync({
  roomConnection,
  sessionId,
  videoRef,
  streamOriginRef,
  appliedCommandIdRef,
}: UseWatchTogetherPlaybackSyncOptions): UseWatchTogetherPlaybackSyncResult {
  const connectionState = roomConnection.connectionState;
  const room = roomConnection.room;
  const transportCommand = roomConnection.transportCommand;
  const serverTimeOffsetMs = roomConnection.serverTimeOffsetMs;
  const attachedSessionId = room?.attached_session_id ?? null;
  const roomConnected = room !== null;
  const roomPlaybackState = room?.playback_state ?? null;
  const roomPhase = room?.phase ?? null;
  const sendRoomMessage = roomConnection.sendRoomMessage;
  const waitingStateRef = useRef<"idle" | "buffering" | "ready">("idle");
  const bufferingTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cancelBuffering = useCallback(() => {
    if (bufferingTimerRef.current !== null) {
      clearTimeout(bufferingTimerRef.current);
      bufferingTimerRef.current = null;
    }
  }, []);
  useEffect(() => {
    const video = videoRef.current;
    const recovered = () => {
      if (video && !video.seeking && video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA) {
        cancelBuffering();
        if (roomPlaybackState !== "waiting" && waitingStateRef.current === "buffering") {
          waitingStateRef.current = "idle";
        }
      }
    };
    video?.addEventListener("canplay", recovered);
    video?.addEventListener("playing", recovered);
    video?.addEventListener("timeupdate", recovered);
    video?.addEventListener("seeked", recovered);
    return () => {
      cancelBuffering();
      video?.removeEventListener("canplay", recovered);
      video?.removeEventListener("playing", recovered);
      video?.removeEventListener("timeupdate", recovered);
      video?.removeEventListener("seeked", recovered);
    };
  }, [
    cancelBuffering,
    connectionState,
    attachedSessionId,
    roomPhase,
    roomPlaybackState,
    room?.room_id,
    room?.selection_revision,
    sessionId,
    transportCommand?.command_id,
    videoRef,
  ]);

  useEffect(() => {
    waitingStateRef.current = "idle";
  }, [
    attachedSessionId,
    connectionState,
    roomPhase,
    room?.room_id,
    room?.playback_state,
    room?.selection_revision,
    sessionId,
    transportCommand?.command_id,
  ]);

  useEffect(() => {
    if (!sessionId || connectionState !== "connected") {
      return;
    }

    sendRoomMessage({ type: "attach_session", session_id: sessionId });
  }, [connectionState, sendRoomMessage, sessionId]);

  useEffect(() => {
    if (!sessionId || connectionState !== "connected") {
      return;
    }

    const intervalId = window.setInterval(() => {
      const video = videoRef.current;
      if (!video || attachedSessionId !== sessionId) {
        return;
      }
      if (transportCommand?.session_id === sessionId) {
        const localExecuteAt = Date.parse(transportCommand.execute_at) - serverTimeOffsetMs;
        if (
          Number.isFinite(localExecuteAt) &&
          localExecuteAt + pendingCommandQuietPeriodMs > Date.now()
        ) {
          return;
        }
      }

      sendRoomMessage({
        type: "state_report",
        session_id: sessionId,
        position_seconds: toMediaTime(video.currentTime, streamOriginRef.current),
        is_paused: video.paused,
      });
    }, stateReportIntervalMs);

    return () => {
      window.clearInterval(intervalId);
    };
  }, [
    attachedSessionId,
    connectionState,
    sendRoomMessage,
    serverTimeOffsetMs,
    sessionId,
    streamOriginRef,
    transportCommand,
    videoRef,
  ]);

  const requestTransport = useCallback(
    (action: "play" | "pause" | "seek", positionSeconds: number, isPaused: boolean) => {
      if (
        connectionState !== "connected" ||
        !roomConnected ||
        !sessionId ||
        attachedSessionId !== sessionId
      ) {
        return { ok: false };
      }
      return sendRoomMessage({
        type: "transport_request",
        action,
        position_seconds: positionSeconds,
        is_paused: isPaused,
      });
    },
    [attachedSessionId, connectionState, roomConnected, sendRoomMessage, sessionId],
  );

  const reportReady = useCallback(() => {
    cancelBuffering();
    const video = videoRef.current;
    const command = transportCommand;
    if (
      connectionState !== "connected" ||
      !roomConnected ||
      !sessionId ||
      attachedSessionId !== sessionId ||
      roomPlaybackState !== "waiting" ||
      waitingStateRef.current === "ready" ||
      !video ||
      video.seeking ||
      video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA ||
      !command ||
      command.playback_state !== "waiting" ||
      command.selection_revision !== room?.selection_revision ||
      (command.session_id && command.session_id !== sessionId) ||
      appliedCommandIdRef.current !== command.command_id
    ) {
      return { ok: false };
    }

    const position = Math.max(0, toMediaTime(video.currentTime, streamOriginRef.current));
    // A canplay event can still belong to the stream a room seek replaces.
    if (
      command.action === "seek" &&
      Math.abs(position - command.position_seconds) > readySeekToleranceSeconds
    ) {
      return { ok: false };
    }
    const result = sendRoomMessage({
      type: "ready",
      command_id: command.command_id,
      session_id: sessionId,
      position_seconds: position,
      is_paused: video.paused,
    });
    if (result.ok) {
      waitingStateRef.current = "ready";
    }
    return result;
  }, [
    attachedSessionId,
    appliedCommandIdRef,
    cancelBuffering,
    connectionState,
    roomConnected,
    roomPlaybackState,
    room?.selection_revision,
    sendRoomMessage,
    sessionId,
    streamOriginRef,
    transportCommand,
    videoRef,
  ]);

  const reportBuffering = useCallback(
    (positionSeconds?: number, isPaused?: boolean) => {
      const video = videoRef.current;
      if (
        connectionState !== "connected" ||
        !roomConnected ||
        !sessionId ||
        attachedSessionId !== sessionId ||
        roomPhase !== "playing" ||
        roomPlaybackState !== "playing" ||
        waitingStateRef.current === "buffering" ||
        !video
      ) {
        return { ok: false };
      }

      if (bufferingTimerRef.current !== null) return { ok: false };
      bufferingTimerRef.current = setTimeout(() => {
        bufferingTimerRef.current = null;
        // A stalled download can leave plenty of playable media buffered.
        if (!video.seeking && video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA) return;
        const result = sendRoomMessage({
          type: "buffering",
          session_id: sessionId,
          position_seconds: Math.max(
            0,
            positionSeconds ?? toMediaTime(video.currentTime, streamOriginRef.current),
          ),
          is_paused: isPaused ?? video.paused,
        });
        if (result.ok) waitingStateRef.current = "buffering";
      }, bufferingGraceMs);
      return { ok: true };
    },
    [
      attachedSessionId,
      connectionState,
      roomConnected,
      roomPhase,
      roomPlaybackState,
      sendRoomMessage,
      sessionId,
      streamOriginRef,
      videoRef,
    ],
  );

  // Stable identity so consumers (e.g. VideoPlayer's video-event-listener
  // effect) don't re-run on every room snapshot.
  return useMemo(
    () => ({
      attachedSessionId,
      requestTransport,
      reportReady,
      reportBuffering,
    }),
    [attachedSessionId, requestTransport, reportReady, reportBuffering],
  );
}
