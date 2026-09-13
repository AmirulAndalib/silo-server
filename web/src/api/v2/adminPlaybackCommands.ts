import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import { randomUUID } from "@/lib/uuid";
import { v2, type V2Result } from "./request";

/**
 * Sequenced administrator playback commands (pause, resume, stop, message).
 *
 * Every command carries an ordered identity the server applies once per
 * session: a client-allocated `command_id` and a `sequence` that must rise
 * within the session. A retry preserves the exact identity and body, so the
 * server replays the recorded receipt instead of dispatching again, and a
 * delayed retry that lands after a newer command is refused as stale rather
 * than reverting the newer playback state. Sequences are allocated per session
 * from a monotonic clock so two commands issued from this browser never share
 * one, even across reloads within the same millisecond boundary.
 */

export type AdminPlaybackCommandAction = "pause" | "resume" | "stop" | "message";

export type AdminPlaybackCommandReceipt =
  V2Result<"POST /api/v2/admin/sessions/{session_id}/pause">;

export type AdminPlaybackCommandIdentity = {
  command_id: string;
  sequence: number;
};

/** Terminate is a separate row with a pending decision; it is deliberately absent. */
const sequencedActions: ReadonlySet<AdminPlaybackCommandAction> = new Set([
  "pause",
  "resume",
  "stop",
  "message",
]);

const lastSequenceBySession = new Map<string, number>();

/** Allocate one ordered command identity for a session. Call once per intended command. */
export function allocateAdminPlaybackCommand(sessionId: string): AdminPlaybackCommandIdentity {
  const previous = lastSequenceBySession.get(sessionId) ?? 0;
  const sequence = Math.max(previous + 1, Date.now());
  lastSequenceBySession.set(sessionId, sequence);
  return { command_id: randomUUID(), sequence };
}

export function captureAdminPlaybackCommandAuthority() {
  const context = captureProfileRequestContext();
  if (!context) throw new StaleApiRequestContextError();
  return context;
}

function requireAuthority(context: ProfileRequestContextSnapshot) {
  if (!isCapturedProfileAuthorityActive(context)) throw new StaleApiRequestContextError();
}

export type AdminPlaybackCommandRequest = {
  sessionId: string;
  action: AdminPlaybackCommandAction;
  identity: AdminPlaybackCommandIdentity;
  reason?: string;
  deadlineMs?: number;
  /** Required for `message`; ignored for the other actions. */
  title?: string;
  message?: string;
};

/**
 * Send one sequenced command under the captured administrator authority. The
 * request is never retried automatically after an uncertain response; a caller
 * that retries must pass the same identity so the server replays the receipt.
 */
export async function sendAdminPlaybackCommand(
  request: AdminPlaybackCommandRequest,
  profileContext = captureAdminPlaybackCommandAuthority(),
): Promise<AdminPlaybackCommandReceipt> {
  if (!sequencedActions.has(request.action)) {
    throw new Error(`Unsupported session command: ${request.action}`);
  }
  if (!Number.isSafeInteger(request.identity.sequence) || request.identity.sequence < 1) {
    throw new Error("A command sequence must be a positive integer.");
  }
  requireAuthority(profileContext);
  const identity = {
    command_id: request.identity.command_id,
    sequence: request.identity.sequence,
    ...(request.reason ? { reason: request.reason } : {}),
    ...(request.deadlineMs ? { deadline_ms: request.deadlineMs } : {}),
  };
  const path = { session_id: request.sessionId };
  const options = { path, profileContext, retryAuthentication: false } as const;
  let receipt: AdminPlaybackCommandReceipt;
  switch (request.action) {
    case "pause":
      receipt = await v2("POST /api/v2/admin/sessions/{session_id}/pause", {
        ...options,
        body: identity,
      });
      break;
    case "resume":
      receipt = await v2("POST /api/v2/admin/sessions/{session_id}/resume", {
        ...options,
        body: identity,
      });
      break;
    case "stop":
      receipt = await v2("POST /api/v2/admin/sessions/{session_id}/stop", {
        ...options,
        body: identity,
      });
      break;
    case "message": {
      const message = request.message?.trim() ?? "";
      if (!message) throw new Error("Message is required");
      receipt = await v2("POST /api/v2/admin/sessions/{session_id}/message", {
        ...options,
        body: { ...identity, message, ...(request.title ? { title: request.title } : {}) },
      });
      break;
    }
  }
  requireAuthority(profileContext);
  if (receipt.command_id !== request.identity.command_id) {
    throw new Error("The server answered a different command identity.");
  }
  return receipt;
}

export async function getAdminPlaybackCommandCapabilities(
  profileContext = captureAdminPlaybackCommandAuthority(),
) {
  requireAuthority(profileContext);
  const capabilities = await v2("GET /api/v2/admin/sessions/command-capabilities", {
    profileContext,
  });
  requireAuthority(profileContext);
  return capabilities;
}

export type AdminPlaybackTerminateReceipt =
  V2Result<"POST /api/v2/admin/sessions/{session_id}/terminate">;

/**
 * Terminate revokes the session's playback authority durably first, then
 * notifies the client as best effort. The receipt reports both facts; a
 * repeat converges without dispatching again, so the request may be retried
 * after an uncertain response.
 */
export async function terminateAdminPlaybackSession(
  sessionId: string,
  reason?: string,
  profileContext = captureAdminPlaybackCommandAuthority(),
): Promise<AdminPlaybackTerminateReceipt> {
  requireAuthority(profileContext);
  const receipt = await v2("POST /api/v2/admin/sessions/{session_id}/terminate", {
    path: { session_id: sessionId },
    body: reason ? { reason } : {},
    profileContext,
  });
  requireAuthority(profileContext);
  if (receipt.session_id !== sessionId) {
    throw new Error("The server answered for a different playback session.");
  }
  return receipt;
}
