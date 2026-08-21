-- +goose Up
-- Admin accounts are never members of an access group: group ceilings (stream
-- caps, library lists) must not apply to the server operator. Create has always
-- left admins ungrouped, but until now an account promoted to admin kept its
-- group, and an existing admin could be placed in one. Clear those rows, bump
-- the policy revision so cached session policy is re-read, and make the
-- invariant a constraint so no write path can reintroduce it.
UPDATE users
SET access_group_id = NULL,
    access_policy_revision = access_policy_revision + 1,
    updated_at = NOW()
WHERE role = 'admin' AND access_group_id IS NOT NULL;

ALTER TABLE users
    ADD CONSTRAINT users_admin_ungrouped CHECK (role <> 'admin' OR access_group_id IS NULL);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_admin_ungrouped;
-- The cleared group memberships are not recoverable.
