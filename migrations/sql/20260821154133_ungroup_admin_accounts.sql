-- +goose Up
-- Admin accounts are never members of an access group: group ceilings (stream
-- caps, library lists) must not apply to the server operator. Create has always
-- left admins ungrouped, but until now an account promoted to admin kept its
-- group, and an existing admin could be placed in one. Clear those rows and
-- bump the policy revision so cached session policy is re-read.
UPDATE users
SET access_group_id = NULL,
    access_policy_revision = access_policy_revision + 1,
    updated_at = NOW()
WHERE role = 'admin' AND access_group_id IS NOT NULL;

-- +goose Down
-- Data-only backfill; the previous group memberships are not recoverable.
SELECT 1;
