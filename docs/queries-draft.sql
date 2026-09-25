-- DRAFT: permission-bearing queries for docs/schema-draft.sql.
-- These move into internal/db/queries.sql when the schema is wired up.
-- Each write returns rows affected; 0 means "not allowed or not found".

-- name: AssignPartToSlot :execrows
-- The caller must own the slot's machine. Their own parts can move freely;
-- another user's part must be owned (bought), not already in a slot, and
-- belong to someone who hasn't left.
UPDATE parts SET slot_id = sqlc.arg(slot_id), updated_at = CURRENT_TIMESTAMP
WHERE parts.id = sqlc.arg(part_id)
  AND EXISTS (
    SELECT 1 FROM machine_slots s JOIN machines m ON m.id = s.machine_id
    WHERE s.id = sqlc.arg(slot_id) AND m.owner_id = sqlc.arg(user_id)
  )
  AND (
    parts.owner_id = sqlc.arg(user_id)
    OR (
      parts.status = 'owned' AND parts.slot_id IS NULL
      AND EXISTS (SELECT 1 FROM users u WHERE u.id = parts.owner_id AND u.deleted_at IS NULL)
    )
  );

-- name: UnassignPart :execrows
-- Either the part's owner or the owner of the machine it sits in.
UPDATE parts SET slot_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE parts.id = sqlc.arg(part_id)
  AND (parts.owner_id = sqlc.arg(user_id) OR parts.slot_id IN (
    SELECT s.id FROM machine_slots s JOIN machines m ON m.id = s.machine_id
    WHERE m.owner_id = sqlc.arg(user_id)
  ));

-- name: ListSlotsByMachine :many
SELECT machine_slots.id, machine_slots.category,
       parts.id AS part_id, parts.name AS part_name, parts.status AS part_status,
       users.username AS part_owner_name,
       CASE
         WHEN parts.id IS NULL THEN 'empty'
         WHEN parts.status = 'wanted' THEN 'unpurchased'
         WHEN parts.owner_id = machines.owner_id THEN 'own'
         WHEN users.deleted_at IS NOT NULL THEN 'former_member'
         ELSE 'borrowed'
       END AS part_source
FROM machine_slots
JOIN machines ON machines.id = machine_slots.machine_id
LEFT JOIN parts ON parts.slot_id = machine_slots.id
LEFT JOIN users ON users.id = parts.owner_id
WHERE machine_slots.machine_id = ?
ORDER BY machine_slots.position, machine_slots.id;

-- name: ListMachines :many
-- Machines of users who haven't left, newest first.
SELECT machines.* FROM machines
JOIN users ON users.id = machines.owner_id
WHERE users.deleted_at IS NULL
ORDER BY machines.created_at DESC, machines.id DESC;

-- name: UpsertDiscordUser :one
-- Called on every Discord login: creates the user, refreshes their profile,
-- and restores them if they had left.
INSERT INTO users (discord_id, username, display_name, avatar_hash, last_login_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (discord_id) DO UPDATE SET
    username = excluded.username,
    display_name = excluded.display_name,
    avatar_hash = excluded.avatar_hash,
    last_login_at = CURRENT_TIMESTAMP,
    deleted_at = NULL
RETURNING *;

-- name: SoftDeleteUser :execrows
UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted_at IS NULL;

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions WHERE user_id = ?;
