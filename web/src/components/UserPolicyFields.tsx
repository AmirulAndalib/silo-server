import { useId } from "react";

import type { AdminUser, AdminUserEffectivePolicy, Library } from "@/api/types";
import { LibraryAccessSelector } from "@/components/LibraryAccessSelector";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  PLAYBACK_QUALITY_OPTIONS,
  formatPlaybackQualityPreset,
  playbackQualityPresetFromValue,
  playbackQualityValueFromPreset,
  type PlaybackQualityPreset,
} from "@/lib/playback-quality";

// Per-user policy overrides. null = inherit the access group's value; a
// concrete value is an explicit override in either direction.
export interface UserPolicyState {
  libraryIDs: number[] | null;
  maxPlaybackQuality: string | null;
  maxStreams: number | null;
  maxTranscodes: number | null;
  transcodeAllowed: boolean | null;
  audioTranscodeAllowed: boolean | null;
  downloadAllowed: boolean | null;
  downloadTranscodeAllowed: boolean | null;
  requestsAllowed: boolean | null;
}

export function policyStateFromUser(user: AdminUser | null): UserPolicyState {
  return {
    libraryIDs: user?.library_ids ?? null,
    maxPlaybackQuality: user?.max_playback_quality ?? null,
    maxStreams: user?.max_streams ?? null,
    maxTranscodes: user?.max_transcodes ?? null,
    transcodeAllowed: user?.transcode_allowed ?? null,
    audioTranscodeAllowed: user?.audio_transcode_allowed ?? null,
    downloadAllowed: user?.download_allowed ?? null,
    downloadTranscodeAllowed: user?.download_transcode_allowed ?? null,
    requestsAllowed: user?.requests_allowed ?? null,
  };
}

// Update payload: every policy field is sent explicitly — a value stores an
// override, null clears it back to inherit.
export function policyUpdateFields(state: UserPolicyState) {
  return {
    library_ids: state.libraryIDs,
    max_playback_quality: state.maxPlaybackQuality,
    max_streams: state.maxStreams,
    max_transcodes: state.maxTranscodes,
    transcode_allowed: state.transcodeAllowed,
    audio_transcode_allowed: state.audioTranscodeAllowed,
    download_allowed: state.downloadAllowed,
    download_transcode_allowed: state.downloadTranscodeAllowed,
    requests_allowed: state.requestsAllowed,
  };
}

// Create payload: only overridden fields are sent; absent fields inherit.
export function policyCreateFields(state: UserPolicyState) {
  return {
    ...(state.libraryIDs !== null ? { library_ids: state.libraryIDs } : {}),
    ...(state.maxPlaybackQuality !== null
      ? { max_playback_quality: state.maxPlaybackQuality }
      : {}),
    ...(state.maxStreams !== null ? { max_streams: state.maxStreams } : {}),
    ...(state.maxTranscodes !== null ? { max_transcodes: state.maxTranscodes } : {}),
    ...(state.transcodeAllowed !== null ? { transcode_allowed: state.transcodeAllowed } : {}),
    ...(state.audioTranscodeAllowed !== null
      ? { audio_transcode_allowed: state.audioTranscodeAllowed }
      : {}),
    ...(state.downloadAllowed !== null ? { download_allowed: state.downloadAllowed } : {}),
    ...(state.downloadTranscodeAllowed !== null
      ? { download_transcode_allowed: state.downloadTranscodeAllowed }
      : {}),
    ...(state.requestsAllowed !== null ? { requests_allowed: state.requestsAllowed } : {}),
  };
}

interface PolicyContext {
  state: UserPolicyState;
  onChange: (state: UserPolicyState) => void;
  // The resolved policy from the server, used to show what an inheriting
  // field currently evaluates to. Absent on the create form.
  effective?: AdminUserEffectivePolicy;
}

function inheritHint(effectiveText: string | undefined): string {
  return effectiveText === undefined ? "Inherited from group" : `Inherited: ${effectiveText}`;
}

const INHERIT = "inherit" as const;

function BooleanPolicyRow({
  label,
  description,
  value,
  onValueChange,
  effectiveValue,
}: {
  label: string;
  description?: string;
  value: boolean | null;
  onValueChange: (value: boolean | null) => void;
  effectiveValue?: boolean;
}) {
  const id = useId();
  const selectValue = value === null ? INHERIT : value ? "allowed" : "blocked";
  return (
    <div className="border-border flex items-center justify-between gap-3 rounded-md border px-3 py-2">
      <div className="min-w-0">
        <Label htmlFor={id}>{label}</Label>
        {description && <p className="text-muted-foreground text-xs">{description}</p>}
      </div>
      <Select
        value={selectValue}
        onValueChange={(next) => onValueChange(next === INHERIT ? null : next === "allowed")}
      >
        <SelectTrigger id={id} className="w-40 shrink-0">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={INHERIT}>
            {inheritHint(
              effectiveValue === undefined ? undefined : effectiveValue ? "Allowed" : "Not allowed",
            )}
          </SelectItem>
          <SelectItem value="allowed">Allowed</SelectItem>
          <SelectItem value="blocked">Not allowed</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}

function LimitPolicyField({
  label,
  value,
  onValueChange,
  effectiveValue,
}: {
  label: string;
  value: number | null;
  onValueChange: (value: number | null) => void;
  effectiveValue?: number;
}) {
  const id = useId();
  const overrideId = `${id}-override`;
  const overridden = value !== null;
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <Label htmlFor={id}>{label}</Label>
        <div className="flex items-center gap-2">
          <Label htmlFor={overrideId} className="text-muted-foreground text-xs">
            Override
          </Label>
          <Switch
            id={overrideId}
            checked={overridden}
            onCheckedChange={(checked) => onValueChange(checked ? (effectiveValue ?? 0) : null)}
          />
        </div>
      </div>
      {overridden ? (
        <>
          <Input
            id={id}
            type="number"
            min={0}
            value={value}
            onChange={(event) => onValueChange(Math.max(0, Number(event.target.value)))}
          />
          <p className="text-muted-foreground text-xs">0 = unlimited</p>
        </>
      ) : (
        <p className="text-muted-foreground border-border rounded-md border border-dashed px-3 py-2 text-sm">
          {inheritHint(
            effectiveValue === undefined
              ? undefined
              : effectiveValue === 0
                ? "Unlimited"
                : String(effectiveValue),
          )}
        </p>
      )}
    </div>
  );
}

// Access-tab policy fields: library scope plus the download/request gates.
export function PolicyAccessFields({
  state,
  onChange,
  effective,
  libraries,
}: PolicyContext & { libraries: Library[] }) {
  return (
    <>
      <LibraryAccessSelector
        libraries={libraries}
        value={state.libraryIDs}
        onChange={(libraryIDs) => onChange({ ...state, libraryIDs })}
        allLabel="Inherit from group"
        emptyHint={inheritHint(
          effective === undefined
            ? undefined
            : effective.library_ids === null
              ? "All libraries"
              : `${effective.library_ids.length} libraries`,
        )}
      />
      <div className="grid gap-2 sm:grid-cols-2">
        <BooleanPolicyRow
          label="Downloads"
          value={state.downloadAllowed}
          onValueChange={(downloadAllowed) => onChange({ ...state, downloadAllowed })}
          effectiveValue={effective?.download_allowed}
        />
        <BooleanPolicyRow
          label="Download Transcodes"
          value={state.downloadTranscodeAllowed}
          onValueChange={(downloadTranscodeAllowed) =>
            onChange({ ...state, downloadTranscodeAllowed })
          }
          effectiveValue={effective?.download_transcode_allowed}
        />
      </div>
      <BooleanPolicyRow
        label="Media Requests"
        description="Request new movies and series when requests are enabled."
        value={state.requestsAllowed}
        onValueChange={(requestsAllowed) => onChange({ ...state, requestsAllowed })}
        effectiveValue={effective?.requests_allowed}
      />
    </>
  );
}

// Limits-tab policy fields: stream/transcode ceilings and the quality gate.
export function PolicyLimitFields({ state, onChange, effective }: PolicyContext) {
  const qualityId = useId();
  const qualityValue: PlaybackQualityPreset | typeof INHERIT =
    state.maxPlaybackQuality === null
      ? INHERIT
      : playbackQualityPresetFromValue(state.maxPlaybackQuality);
  return (
    <>
      <div className="grid gap-3 sm:grid-cols-2">
        <LimitPolicyField
          label="Max Streams"
          value={state.maxStreams}
          onValueChange={(maxStreams) => onChange({ ...state, maxStreams })}
          effectiveValue={effective?.max_streams}
        />
        <LimitPolicyField
          label="Max Transcodes"
          value={state.maxTranscodes}
          onValueChange={(maxTranscodes) => onChange({ ...state, maxTranscodes })}
          effectiveValue={effective?.max_transcodes}
        />
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        <BooleanPolicyRow
          label="Video Transcoding"
          value={state.transcodeAllowed}
          onValueChange={(transcodeAllowed) => onChange({ ...state, transcodeAllowed })}
          effectiveValue={effective?.transcode_allowed}
        />
        <BooleanPolicyRow
          label="Audio Transcoding"
          description="Audio conversion without video encoding."
          value={state.audioTranscodeAllowed}
          onValueChange={(audioTranscodeAllowed) => onChange({ ...state, audioTranscodeAllowed })}
          effectiveValue={effective?.audio_transcode_allowed}
        />
      </div>
      <div className="space-y-1">
        <Label htmlFor={qualityId}>Max Playback Quality</Label>
        <Select
          value={qualityValue}
          onValueChange={(value) =>
            onChange({
              ...state,
              maxPlaybackQuality:
                value === INHERIT
                  ? null
                  : playbackQualityValueFromPreset(value as PlaybackQualityPreset),
            })
          }
        >
          <SelectTrigger id={qualityId} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={INHERIT}>
              {inheritHint(
                effective === undefined
                  ? undefined
                  : formatPlaybackQualityPreset(effective.max_playback_quality),
              )}
            </SelectItem>
            {PLAYBACK_QUALITY_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-muted-foreground text-xs">
          {qualityValue === INHERIT
            ? "Uses the access group's quality ceiling."
            : PLAYBACK_QUALITY_OPTIONS.find((option) => option.value === qualityValue)?.description}
        </p>
      </div>
    </>
  );
}
