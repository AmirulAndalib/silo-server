import { useMemo, useRef, useState } from "react";
import { AlertTriangle, Plus, RotateCcw, Trash2 } from "lucide-react";

import type { ConnectionCheckResponse } from "@/api/types";
import { ConnectionCheckAction } from "@/components/admin/ConnectionCheckAction";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SecretField } from "@/components/settings/SecretField";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useCheckAdminSettingsConnection } from "@/hooks/queries/admin/settings";
import { useRestartKeys, type RestartKeyMatcher } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";

import { FieldGroup } from "./FieldGroup";
import { SaveBar } from "./SaveBar";
import { SettingField } from "./SettingField";
import { USER_DATABASE_BACKEND_OPTIONS } from "./databaseSettingOptions";
import {
  LOG_LEVEL_OPTIONS,
  OPSLOG_BUCKET_POLICIES_KEY,
  OPSLOG_MAX_ROWS_KEY,
  OPSLOG_MAX_SIZE_MB_KEY,
  OPSLOG_RETENTION_DAYS_KEY,
  appendBucketRow,
  bucketRowsFromRaw,
  recommendedBucketRows,
  removeBucketRow,
  serializeBucketRows,
  updateBucketRow,
  type LogRetentionBucketPolicy,
  type LogRetentionBucketRow,
} from "./logRetentionPolicy";

type SettingsForm = ReturnType<typeof useSettingsForm>;

const REDIS_KEYS = ["redis.url"];

const DATABASE_KEYS = [
  "database.max_connections",
  "userdb.backend",
  "userdb.pool_max_open",
  "userdb.idle_timeout",
];

const PUBLIC_S3_KEYS = [
  "s3.public_endpoint",
  "s3.public_region",
  "s3.public_path_style",
  "s3.public_bucket",
  "s3.public_key_prefix",
  "s3.public_access_key",
  "s3.public_secret_key",
  "s3.public_read_endpoint",
  "s3.public_url_auth",
  "s3.public_token_secret",
  "s3.public_token_param",
  "s3.public_token_ttl",
];

// Changing any of these moves where cached artwork objects live. Silo detects
// that change after restart but requires an explicit manual reconcile so an
// incomplete bucket migration cannot rewrite the artwork catalog.
const PUBLIC_S3_IDENTITY_KEYS = ["s3.public_endpoint", "s3.public_bucket", "s3.public_key_prefix"];

const PRIVATE_S3_KEYS = [
  "s3.private_endpoint",
  "s3.private_region",
  "s3.private_path_style",
  "s3.private_bucket",
  "s3.private_key_prefix",
  "s3.private_access_key",
  "s3.private_secret_key",
];

const LOG_KEYS = [
  OPSLOG_RETENTION_DAYS_KEY,
  OPSLOG_MAX_ROWS_KEY,
  OPSLOG_MAX_SIZE_MB_KEY,
  OPSLOG_BUCKET_POLICIES_KEY,
  "policy.decision_log_retention_days",
  "policy.decision_log_verbosity",
  "policy.decision_log_scope_sample_rate",
];

const KEYS = [...REDIS_KEYS, ...DATABASE_KEYS, ...PUBLIC_S3_KEYS, ...PRIVATE_S3_KEYS, ...LOG_KEYS];

/**
 * Shared editing state for every credential on the tab. A credential can only
 * be replaced through an explicit Replace click, and every editor is frozen
 * while a save is in flight so a late keystroke cannot ride along with it.
 */
interface SecretEditors {
  isEditing: (key: string) => boolean;
  beginReplace: (key: string) => void;
  keepSaved: (key: string) => void;
  setSecret: (key: string, value: string) => void;
  disabled: boolean;
}

function RedisGroup({
  form,
  restartKeys,
  secrets,
}: {
  form: SettingsForm;
  restartKeys: RestartKeyMatcher;
  secrets: SecretEditors;
}) {
  const checkConnection = useCheckAdminSettingsConnection();
  const [connectionResult, setConnectionResult] = useState<ConnectionCheckResponse | null>(null);
  const redisUrl = form.getValue("redis.url");
  const managedByEnv = form.sensitiveManagedByEnv.includes("redis.url");
  const configured = form.sensitiveConfigured.includes("redis.url");
  const [enabledOverride, setEnabledOverride] = useState<boolean | null>(null);
  // Saving or discarding clears the toggle override so it follows the stored
  // URL again. Adjusting during render (rather than in an effect) keeps the
  // override alive while the admin is still editing.
  const [lastDirtyCount, setLastDirtyCount] = useState(form.dirtyCount);
  if (lastDirtyCount !== form.dirtyCount) {
    setLastDirtyCount(form.dirtyCount);
    if (form.dirtyCount === 0) setEnabledOverride(null);
  }
  const enabled = enabledOverride ?? (redisUrl.trim() !== "" || configured);

  async function handleCheckConnection() {
    try {
      setConnectionResult(
        await checkConnection.mutateAsync({
          kind: "redis",
          body: form.buildConnectionCheckRequest(REDIS_KEYS),
        }),
      );
    } catch (error) {
      setConnectionResult({
        success: false,
        message: error instanceof Error ? error.message : "Connection check failed.",
      });
    }
  }

  return (
    <FieldGroup label="Redis">
      {managedByEnv && (
        <div className="border-border/70 flex flex-col gap-2 border-b py-3">
          <div className="flex items-center gap-2">
            <Badge variant="outline">Managed by environment</Badge>
          </div>
          <p className="text-muted-foreground text-sm">
            Redis is configured by the <code>REDIS_URL</code> environment variable. Change your
            deployment configuration and restart the server to update or disable Redis.
          </p>
        </div>
      )}
      <SettingField
        label="Use Redis"
        type="toggle"
        hint={
          managedByEnv
            ? "This setting is controlled by REDIS_URL"
            : "Shares sessions and caches between servers. A single-server install works without it."
        }
        value={enabled ? "true" : "false"}
        onChange={(value) => {
          if (value === "true") {
            setEnabledOverride(true);
            form.resetValue("redis.url");
            return;
          }
          setEnabledOverride(false);
          form.setValue("redis.url", "");
        }}
        disabled={managedByEnv}
        restartRequired={restartKeys.has("redis.url")}
      />
      {enabled && (
        <>
          <SecretField
            label="Connection URL"
            value={redisUrl}
            configured={configured}
            editing={secrets.isEditing("redis.url")}
            onReplace={() => secrets.beginReplace("redis.url")}
            onKeep={() => secrets.keepSaved("redis.url")}
            onChange={(v) => secrets.setSecret("redis.url", v)}
            hint={managedByEnv ? "Value supplied by REDIS_URL" : "redis://host:6379"}
            disabled={managedByEnv || secrets.disabled}
            restartRequired={restartKeys.has("redis.url")}
          />
          <ConnectionCheckAction
            onClick={handleCheckConnection}
            result={connectionResult}
            isPending={checkConnection.isPending}
            disabled={form.isSaving || managedByEnv}
          />
        </>
      )}
    </FieldGroup>
  );
}

function S3Group({
  form,
  restartKeys,
  secrets,
  scope,
  label,
  description,
  checkKind,
}: {
  form: SettingsForm;
  restartKeys: RestartKeyMatcher;
  secrets: SecretEditors;
  scope: "public" | "private";
  label: string;
  description: string;
  checkKind: "s3_public" | "s3_private";
}) {
  const checkConnection = useCheckAdminSettingsConnection();
  const [connectionResult, setConnectionResult] = useState<ConnectionCheckResponse | null>(null);
  const keys = scope === "public" ? PUBLIC_S3_KEYS : PRIVATE_S3_KEYS;
  const key = (suffix: string) => `s3.${scope}_${suffix}`;
  const urlAuth = form.getValue("s3.public_url_auth") || "presigned";

  const advancedKeys =
    scope === "public"
      ? [
          "s3.public_region",
          "s3.public_path_style",
          "s3.public_key_prefix",
          "s3.public_url_auth",
          "s3.public_read_endpoint",
          "s3.public_token_secret",
          "s3.public_token_param",
          "s3.public_token_ttl",
        ]
      : ["s3.private_region", "s3.private_path_style", "s3.private_key_prefix"];
  const advancedCount =
    scope === "public"
      ? 4 + (urlAuth !== "presigned" ? 1 : 0) + (urlAuth === "cloudflare_token" ? 3 : 0)
      : 3;

  async function handleCheckConnection() {
    try {
      setConnectionResult(
        await checkConnection.mutateAsync({
          kind: checkKind,
          body: form.buildConnectionCheckRequest(keys),
        }),
      );
    } catch (error) {
      setConnectionResult({
        success: false,
        message: error instanceof Error ? error.message : "Connection check failed.",
      });
    }
  }

  return (
    <FieldGroup label={label}>
      <p className="text-muted-foreground py-2 text-sm leading-relaxed">{description}</p>
      <SettingField
        label="Endpoint"
        hint="Address of your S3-compatible storage, for example https://s3.us-east-1.amazonaws.com."
        value={form.getValue(key("endpoint"))}
        onChange={(v) => form.setValue(key("endpoint"), v)}
        restartRequired={restartKeys.has(key("endpoint"))}
      />
      <SettingField
        label="Bucket"
        value={form.getValue(key("bucket"))}
        onChange={(v) => form.setValue(key("bucket"), v)}
        restartRequired={restartKeys.has(key("bucket"))}
      />
      {scope === "public" && PUBLIC_S3_IDENTITY_KEYS.some((k) => form.isDirty(k)) && (
        <div className="my-3 flex items-start gap-3 rounded-xl border border-amber-500/20 bg-amber-500/5 p-4">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          <div className="text-[13px] leading-relaxed">
            <p className="font-medium text-amber-500">Storage location change</p>
            <p className="text-muted-foreground mt-1">
              Artwork is cached in this bucket. Silo will not change artwork cache records
              automatically after restart. Copy or migrate the existing bucket objects first, then
              manually run Reconcile Artwork Cache only if you intend every missing record to be
              reset or cleared. Re-downloading those reset provider images is a separate, manual
              Backfill Metadata Images action; normal scheduled caching only processes artwork
              queued by new or changed metadata. Uploaded images (custom posters, collection
              artwork, branding) cannot be re-downloaded.
            </p>
          </div>
        </div>
      )}
      <SecretField
        label="Access Key"
        value={form.getValue(key("access_key"))}
        configured={form.sensitiveConfigured.includes(key("access_key"))}
        editing={secrets.isEditing(key("access_key"))}
        onReplace={() => secrets.beginReplace(key("access_key"))}
        onKeep={() => secrets.keepSaved(key("access_key"))}
        onChange={(v) => secrets.setSecret(key("access_key"), v)}
        disabled={secrets.disabled}
        restartRequired={restartKeys.has(key("access_key"))}
      />
      <SecretField
        label="Secret Key"
        value={form.getValue(key("secret_key"))}
        configured={form.sensitiveConfigured.includes(key("secret_key"))}
        editing={secrets.isEditing(key("secret_key"))}
        onReplace={() => secrets.beginReplace(key("secret_key"))}
        onKeep={() => secrets.keepSaved(key("secret_key"))}
        onChange={(v) => secrets.setSecret(key("secret_key"), v)}
        disabled={secrets.disabled}
        restartRequired={restartKeys.has(key("secret_key"))}
      />
      <ConnectionCheckAction
        onClick={handleCheckConnection}
        result={connectionResult}
        isPending={checkConnection.isPending}
        disabled={form.isSaving}
      />

      <AdvancedSection
        id={`infrastructure.s3.${scope}`}
        count={advancedCount}
        forceOpen={advancedKeys.some((k) => form.isDirty(k))}
      >
        <SettingField
          label="Region"
          hint="Leave blank unless your provider requires one."
          value={form.getValue(key("region"))}
          onChange={(v) => form.setValue(key("region"), v)}
          restartRequired={restartKeys.has(key("region"))}
        />
        <SettingField
          label="Put the bucket name in the URL path"
          type="toggle"
          hint="Needed by MinIO and some self-hosted storage. Amazon S3 and most providers do not use it."
          value={form.getValue(key("path_style"))}
          onChange={(v) => form.setValue(key("path_style"), v)}
          restartRequired={restartKeys.has(key("path_style"))}
        />
        <SettingField
          label="Folder inside the bucket"
          hint="Optional. Stores all Silo objects under this folder. Leave blank to use the bucket root."
          value={form.getValue(key("key_prefix"))}
          onChange={(v) => form.setValue(key("key_prefix"), v)}
          restartRequired={restartKeys.has(key("key_prefix"))}
        />
        {scope === "public" && (
          <>
            <SettingField
              label="How asset links are authorized"
              type="select"
              hint="Signed links work with a private bucket and suit almost every install. The other options need the bucket or CDN to serve files itself."
              value={urlAuth}
              onChange={(v) => form.setValue("s3.public_url_auth", v)}
              options={[
                { value: "presigned", label: "Signed links (recommended)" },
                { value: "public", label: "Anyone with the link" },
                { value: "cloudflare_token", label: "Cloudflare signed token" },
              ]}
              restartRequired={restartKeys.has("s3.public_url_auth")}
            />
            {urlAuth !== "presigned" && (
              <SettingField
                label="Address clients download from"
                hint="https://cdn.example.com"
                value={form.getValue("s3.public_read_endpoint")}
                onChange={(v) => form.setValue("s3.public_read_endpoint", v)}
                restartRequired={restartKeys.has("s3.public_read_endpoint")}
              />
            )}
            {urlAuth === "cloudflare_token" && (
              <>
                <SecretField
                  label="Token Secret"
                  value={form.getValue("s3.public_token_secret")}
                  configured={form.sensitiveConfigured.includes("s3.public_token_secret")}
                  editing={secrets.isEditing("s3.public_token_secret")}
                  onReplace={() => secrets.beginReplace("s3.public_token_secret")}
                  onKeep={() => secrets.keepSaved("s3.public_token_secret")}
                  onChange={(v) => secrets.setSecret("s3.public_token_secret", v)}
                  hint="The signing key configured on the Cloudflare side."
                  disabled={secrets.disabled}
                  restartRequired={restartKeys.has("s3.public_token_secret")}
                />
                <SettingField
                  label="Token query parameter"
                  hint="Name of the query parameter Cloudflare expects, usually verify."
                  value={form.getValue("s3.public_token_param") || "verify"}
                  onChange={(v) => form.setValue("s3.public_token_param", v)}
                  restartRequired={restartKeys.has("s3.public_token_param")}
                />
                <SettingField
                  label="Link lifetime (seconds)"
                  type="number"
                  hint="How long a generated link keeps working."
                  value={form.getValue("s3.public_token_ttl") || "10800"}
                  onChange={(v) => form.setValue("s3.public_token_ttl", v)}
                  restartRequired={restartKeys.has("s3.public_token_ttl")}
                />
              </>
            )}
          </>
        )}
      </AdvancedSection>
    </FieldGroup>
  );
}

function DatabaseGroup({
  form,
  restartKeys,
}: {
  form: SettingsForm;
  restartKeys: RestartKeyMatcher;
}) {
  const userDBBackend = form.getValue("userdb.backend");
  const sqlite = userDBBackend === "sqlite";

  return (
    <FieldGroup label="Database">
      <p className="text-muted-foreground py-2 text-sm leading-relaxed">
        Silo stores its catalog in Postgres. The defaults suit every install up to a busy multi-node
        deployment.
      </p>
      <AdvancedSection
        id="infrastructure.database"
        count={sqlite ? 4 : 2}
        forceOpen={DATABASE_KEYS.some((k) => form.isDirty(k))}
      >
        <SettingField
          label="Maximum Postgres connections"
          type="number"
          hint="Raise this only if the server logs connection-pool waits under load."
          value={form.getValue("database.max_connections")}
          onChange={(v) => form.setValue("database.max_connections", v)}
          restartRequired={restartKeys.has("database.max_connections")}
        />
        <SettingField
          label="Where per-user data is stored"
          type="select"
          hint="Watch progress and personal settings live here. PostgreSQL is the only supported option today."
          options={USER_DATABASE_BACKEND_OPTIONS}
          value={userDBBackend}
          onChange={(v) => form.setValue("userdb.backend", v)}
          restartRequired={restartKeys.has("userdb.backend")}
        />
        {sqlite && (
          <>
            <SettingField
              label="Open files per user"
              type="number"
              hint="How many SQLite connections one user's database may hold open at once."
              value={form.getValue("userdb.pool_max_open")}
              onChange={(v) => form.setValue("userdb.pool_max_open", v)}
              restartRequired={restartKeys.has("userdb.pool_max_open")}
            />
            <SettingField
              label="Close idle user databases after"
              type="duration"
              hint="How long an unused per-user SQLite connection stays open, for example 12h."
              value={form.getValue("userdb.idle_timeout")}
              onChange={(v) => form.setValue("userdb.idle_timeout", v)}
              restartRequired={restartKeys.has("userdb.idle_timeout")}
            />
          </>
        )}
      </AdvancedSection>
    </FieldGroup>
  );
}

function BucketOverridesEditor({
  rows,
  parseError,
  onChange,
  onRestore,
}: {
  rows: LogRetentionBucketRow[];
  parseError: string;
  onChange: (rows: LogRetentionBucketRow[]) => void;
  onRestore: () => void;
}) {
  function edit(id: string, field: keyof LogRetentionBucketPolicy, value: string) {
    onChange(updateBucketRow(rows, id, field, value));
  }

  return (
    <div className="space-y-4 py-3">
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
        <div className="text-muted-foreground text-sm">
          Keep noisy areas such as <span className="font-mono">metadata/info</span> for less time
          than everything else. A limit of <span className="font-mono">0</span> turns that one rule
          off.
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Button type="button" size="sm" variant="outline" onClick={onRestore}>
            <RotateCcw className="size-4" />
            Restore Recommended Rules
          </Button>
          <Button type="button" size="sm" onClick={() => onChange(appendBucketRow(rows))}>
            <Plus className="size-4" />
            Add Rule
          </Button>
        </div>
      </div>

      {parseError ? (
        <div className="border-warning/30 bg-warning/10 text-warning rounded-[1rem] border px-3 py-2 text-sm">
          The saved rules could not be read. The editor loaded the recommended rules so you can
          recover cleanly. Details: {parseError}
        </div>
      ) : null}

      <div className="surface-panel-subtle overflow-x-auto rounded-[1rem]">
        <table className="w-full border-collapse text-sm">
          <thead className="bg-muted/40 text-left">
            <tr>
              <th className="px-3 py-2 font-medium">Component</th>
              <th className="px-3 py-2 font-medium">Level</th>
              <th className="px-3 py-2 font-medium">Days</th>
              <th className="px-3 py-2 font-medium">Max Rows</th>
              <th className="px-3 py-2 font-medium">Max Size (MB)</th>
              <th className="w-[60px] px-3 py-2 font-medium"> </th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={6} className="text-muted-foreground px-3 py-6 text-center">
                  No per-area rules configured.
                </td>
              </tr>
            ) : (
              rows.map((row) => (
                <tr key={row.id} className="border-t">
                  <td className="px-3 py-2">
                    <Input
                      value={row.component}
                      onChange={(event) => edit(row.id, "component", event.target.value)}
                      placeholder="metadata"
                      aria-label={`Component for rule ${row.id}`}
                    />
                  </td>
                  <td className="px-3 py-2">
                    <Select
                      value={row.level}
                      onValueChange={(value) => edit(row.id, "level", value)}
                    >
                      <SelectTrigger className="w-[120px]">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {LOG_LEVEL_OPTIONS.map((level) => (
                          <SelectItem key={level} value={level}>
                            {level}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </td>
                  <td className="px-3 py-2">
                    <Input
                      type="number"
                      min="0"
                      value={String(row.retention_days)}
                      onChange={(event) => edit(row.id, "retention_days", event.target.value)}
                      className="w-[110px]"
                      aria-label={`Days for rule ${row.id}`}
                    />
                  </td>
                  <td className="px-3 py-2">
                    <Input
                      type="number"
                      min="0"
                      value={String(row.max_rows)}
                      onChange={(event) => edit(row.id, "max_rows", event.target.value)}
                      className="w-[140px]"
                      aria-label={`Max rows for rule ${row.id}`}
                    />
                  </td>
                  <td className="px-3 py-2">
                    <Input
                      type="number"
                      min="0"
                      value={String(row.max_size_mb)}
                      onChange={(event) => edit(row.id, "max_size_mb", event.target.value)}
                      className="w-[140px]"
                      aria-label={`Max size for rule ${row.id}`}
                    />
                  </td>
                  <td className="px-3 py-2 text-right">
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="outline"
                      onClick={() => onChange(removeBucketRow(rows, row.id))}
                      aria-label={`Remove ${row.component || "bucket"} rule`}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="text-muted-foreground text-xs leading-5">
        Entries matching a rule are removed oldest-first once they pass it. The overall limits above
        then apply to everything that is left.
      </div>
    </div>
  );
}

function LogsGroup({ form, restartKeys }: { form: SettingsForm; restartKeys: RestartKeyMatcher }) {
  // The bucket rules are one JSON setting edited as a table, so the rows live
  // here while they are dirty and re-hydrate from the saved value otherwise.
  const [draftRows, setDraftRows] = useState<LogRetentionBucketRow[] | null>(null);
  const raw = form.getValue(OPSLOG_BUCKET_POLICIES_KEY);
  const bucketDirty = form.isDirty(OPSLOG_BUCKET_POLICIES_KEY);
  const hydrated = useMemo(() => bucketRowsFromRaw(raw), [raw]);
  // A stale draft can never be shown: it is only reachable while the key is
  // dirty, and the only thing that marks it dirty also sets the draft.
  const rows = bucketDirty && draftRows ? draftRows : hydrated.rows;
  const parseError = bucketDirty ? "" : hydrated.error;

  function commitRows(next: LogRetentionBucketRow[]) {
    setDraftRows(next);
    form.setValue(OPSLOG_BUCKET_POLICIES_KEY, serializeBucketRows(next));
  }

  return (
    <FieldGroup label="Server logs">
      <p className="text-muted-foreground py-2 text-sm leading-relaxed">
        Silo trims its own activity log so it cannot grow without bound. Change these only if the
        log is using too much space, or if you need to keep it for longer. How often the cleanup
        runs is set in Scheduled Tasks.
      </p>
      <AdvancedSection
        id="infrastructure.logs"
        count={7}
        forceOpen={LOG_KEYS.some((k) => form.isDirty(k))}
      >
        <SettingField
          label="Delete log entries older than (days)"
          type="number"
          hint="The oldest entries are removed first."
          value={form.getValue(OPSLOG_RETENTION_DAYS_KEY)}
          onChange={(v) => form.setValue(OPSLOG_RETENTION_DAYS_KEY, v)}
          restartRequired={restartKeys.has(OPSLOG_RETENTION_DAYS_KEY)}
        />
        <SettingField
          label="Maximum log entries"
          type="number"
          hint="Once the log passes this many entries, only the newest are kept."
          value={form.getValue(OPSLOG_MAX_ROWS_KEY)}
          onChange={(v) => form.setValue(OPSLOG_MAX_ROWS_KEY, v)}
          restartRequired={restartKeys.has(OPSLOG_MAX_ROWS_KEY)}
        />
        <SettingField
          label="Maximum log size (MB)"
          type="number"
          hint="Estimated from the stored entries. The oldest are removed when the log grows past this."
          value={form.getValue(OPSLOG_MAX_SIZE_MB_KEY)}
          onChange={(v) => form.setValue(OPSLOG_MAX_SIZE_MB_KEY, v)}
          restartRequired={restartKeys.has(OPSLOG_MAX_SIZE_MB_KEY)}
        />

        <div className="py-3">
          <h3 className="text-sm font-medium">Permission checks</h3>
          <p className="text-muted-foreground mt-1 text-sm leading-relaxed">
            Silo can record why each request was allowed or denied, which is how you find out why a
            user cannot see something.
          </p>
        </div>
        <SettingField
          label="Delete permission records older than (days)"
          type="number"
          value={form.getValue("policy.decision_log_retention_days")}
          onChange={(v) => form.setValue("policy.decision_log_retention_days", v)}
          restartRequired={restartKeys.has("policy.decision_log_retention_days")}
        />
        <SettingField
          label="How much to record"
          type="select"
          hint="Summary keeps the decision only. Full also keeps a sample of the request and the answer, which is larger but easier to debug."
          value={form.getValue("policy.decision_log_verbosity") || "digest"}
          onChange={(v) => form.setValue("policy.decision_log_verbosity", v)}
          options={[
            { value: "digest", label: "Summary" },
            { value: "verbose", label: "Full" },
          ]}
          restartRequired={restartKeys.has("policy.decision_log_verbosity")}
        />
        <SettingField
          label="Record one allowed check in every"
          type="number"
          hint="Allowed checks are frequent, so only a sample is stored. Denials and errors are always recorded."
          value={form.getValue("policy.decision_log_scope_sample_rate")}
          onChange={(v) => form.setValue("policy.decision_log_scope_sample_rate", v)}
          restartRequired={restartKeys.has("policy.decision_log_scope_sample_rate")}
        />

        <div className="py-3">
          <h3 className="text-sm font-medium">Per-area limits</h3>
        </div>
        <BucketOverridesEditor
          rows={rows}
          parseError={parseError}
          onChange={commitRows}
          onRestore={() => commitRows(recommendedBucketRows())}
        />
      </AdvancedSection>
    </FieldGroup>
  );
}

export default function InfrastructureSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();
  const [editingSecretKeys, setEditingSecretKeys] = useState<Set<string>>(new Set());
  const [saveInProgress, setSaveInProgress] = useState(false);
  const saveInProgressRef = useRef(false);

  const secrets: SecretEditors = {
    isEditing: (key) => editingSecretKeys.has(key),
    beginReplace: (key) => {
      if (saveInProgressRef.current) return;
      setEditingSecretKeys((current) => new Set(current).add(key));
    },
    keepSaved: (key) => {
      if (saveInProgressRef.current) return;
      form.resetValue(key);
      setEditingSecretKeys((current) => {
        const next = new Set(current);
        next.delete(key);
        return next;
      });
    },
    setSecret: (key, value) => {
      if (saveInProgressRef.current) return;
      form.setValue(key, value);
    },
    disabled: form.isSaving || saveInProgress,
  };

  async function handleSave() {
    if (saveInProgressRef.current) return;
    saveInProgressRef.current = true;
    setSaveInProgress(true);
    try {
      await form.save();
      setEditingSecretKeys(new Set());
    } catch {
      // The mutation reports the error; keep credential editors open for retry.
    } finally {
      saveInProgressRef.current = false;
      setSaveInProgress(false);
    }
  }

  function handleDiscard() {
    if (saveInProgressRef.current) return;
    form.discard();
    setEditingSecretKeys(new Set());
  }

  if (form.sensitiveStatusError) {
    return (
      <div
        className="flex items-start gap-3 rounded-xl border border-red-500/20 bg-red-500/5 p-4"
        role="alert"
      >
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-500" />
        <div>
          <p className="text-sm font-medium">Protected credential status is unavailable</p>
          <p className="text-muted-foreground mt-1 text-xs">
            Reload this page before editing infrastructure settings.
          </p>
        </div>
      </div>
    );
  }

  if (form.isLoading || !form.sensitiveStatusReady)
    return (
      <div className="space-y-6" role="status" aria-label="Loading settings">
        <Skeleton className="h-8 w-48" />
        <div className="space-y-4">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
        <span className="sr-only">Loading settings</span>
      </div>
    );

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-xl font-semibold tracking-tight">Infrastructure</h2>
          <Badge variant="outline">Advanced</Badge>
        </div>
        <p className="text-muted-foreground text-sm leading-relaxed">
          The services Silo runs on: its cache, its object storage, its database, and how long it
          keeps its own logs.
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <RedisGroup form={form} restartKeys={restartKeys} secrets={secrets} />
        <S3Group
          form={form}
          restartKeys={restartKeys}
          secrets={secrets}
          scope="public"
          label="Public storage"
          description="Holds what clients download directly: artwork, chapter thumbnails, and subtitle files. The bucket itself can stay private; Silo hands out signed links by default."
          checkKind="s3_public"
        />
        <S3Group
          form={form}
          restartKeys={restartKeys}
          secrets={secrets}
          scope="private"
          label="Private storage"
          description="Holds files only the server reads: imports, exports, and internal artifacts."
          checkKind="s3_private"
        />
        <DatabaseGroup form={form} restartKeys={restartKeys} />
        <LogsGroup form={form} restartKeys={restartKeys} />
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={handleSave}
        onDiscard={handleDiscard}
        isSaving={form.isSaving || saveInProgress}
        restartRequired={form.restartRequired}
      />
    </div>
  );
}
