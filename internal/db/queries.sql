-- Queries that change a user's data filter on the acting user, so a write
-- nobody is allowed to make affects 0 rows. :execrows queries report that.

-- Users and sessions --------------------------------------------------------

-- name: UpsertDiscordUser :one
-- Called on every login: creates the user, refreshes their profile, and
-- restores them if they had left.
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

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?);

-- name: GetSessionUser :one
-- The user behind a live session. Users who have left have no live sessions.
SELECT users.* FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = sqlc.arg(token_hash)
  AND sessions.expires_at > sqlc.arg(now)
  AND users.deleted_at IS NULL;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;

-- Machines ------------------------------------------------------------------

-- name: ListMachines :many
-- Machines of users who haven't left, newest first, with slot and price
-- totals for the list page.
SELECT sqlc.embed(machines), users.username AS owner_name,
    (SELECT count(*) FROM machine_slots s WHERE s.machine_id = machines.id) AS total_slots,
    (SELECT count(*) FROM machine_slots s JOIN parts p ON p.slot_id = s.id
     WHERE s.machine_id = machines.id) AS filled_slots,
    CAST((SELECT coalesce(sum(p.price_cents), 0) FROM machine_slots s JOIN parts p ON p.slot_id = s.id
     WHERE s.machine_id = machines.id) AS INTEGER) AS parts_total_cents
FROM machines
JOIN users ON users.id = machines.owner_id
WHERE users.deleted_at IS NULL
ORDER BY machines.created_at DESC, machines.id DESC;

-- name: GetMachine :one
-- A machine whose owner hasn't left.
SELECT sqlc.embed(machines), users.username AS owner_name
FROM machines
JOIN users ON users.id = machines.owner_id
WHERE machines.id = ? AND users.deleted_at IS NULL;

-- name: GetMachineByOwnerAndName :one
SELECT * FROM machines WHERE owner_id = ? AND name = ?;

-- name: CreateMachine :one
INSERT INTO machines (owner_id, name, kind, status, description, location, built_on, purchased_on)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteMachine :execrows
DELETE FROM machines WHERE id = ? AND owner_id = ?;

-- Slots ---------------------------------------------------------------------

-- name: CreateSlot :one
-- Appends a slot after the machine's existing ones.
INSERT INTO machine_slots (machine_id, category, position)
VALUES (
    sqlc.arg(machine_id), sqlc.arg(category),
    (SELECT coalesce(max(position), -1) + 1 FROM machine_slots WHERE machine_id = sqlc.arg(machine_id))
)
RETURNING *;

-- name: GetSlot :one
-- A slot with its machine's owner, for permission checks.
SELECT machine_slots.*, machines.owner_id AS machine_owner_id
FROM machine_slots
JOIN machines ON machines.id = machine_slots.machine_id
WHERE machine_slots.id = ?;

-- name: DeleteSlot :execrows
-- Parts in the slot go back to their owners' inventories (ON DELETE SET NULL).
DELETE FROM machine_slots
WHERE machine_slots.id = sqlc.arg(slot_id)
  AND machine_slots.machine_id IN (SELECT id FROM machines WHERE owner_id = sqlc.arg(user_id));

-- name: ListSlotsByMachine :many
-- part_source labels each slot relative to the machine's owner:
-- empty, unpurchased, own, former_member, or borrowed.
SELECT machine_slots.id, machine_slots.machine_id, machine_slots.category,
       parts.id AS part_id, parts.name AS part_name, parts.model AS part_model,
       parts.status AS part_status, parts.price_cents AS part_price_cents,
       parts.owner_id AS part_owner_id, users.username AS part_owner_name,
       parts.source_url AS part_source_url,
       parts.previous_price_cents AS part_previous_price_cents,
       parts.price_checked_at AS part_price_checked_at,
       parts.missing_from_source_at AS part_missing_from_source_at,
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

-- Parts ---------------------------------------------------------------------

-- name: CreatePart :one
INSERT INTO parts (
    owner_id, slot_id, status, name, category, manufacturer, model,
    price_cents, purchased_from, source_url, price_checked_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListPartsByOwner :many
SELECT * FROM parts WHERE owner_id = ? ORDER BY created_at DESC, id DESC;

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

-- name: DeletePart :execrows
DELETE FROM parts WHERE id = ? AND owner_id = ?;

-- name: MarkPartBought :execrows
-- The owner bought a planned part. Its price becomes what they paid (or
-- stays the last known price), and refreshes leave it alone from now on.
UPDATE parts SET
    status = 'owned',
    price_cents = coalesce(sqlc.narg(price_cents), price_cents),
    previous_price_cents = NULL,
    missing_from_source_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(user_id) AND status = 'wanted';

-- Price refresh -------------------------------------------------------------

-- name: ListRefreshableParts :many
-- The machine's parts that its owner hasn't bought yet and that came from a
-- PCPartPicker list, in slot order.
SELECT parts.* FROM parts
JOIN machine_slots s ON s.id = parts.slot_id
JOIN machines m ON m.id = s.machine_id
WHERE m.id = sqlc.arg(machine_id) AND m.owner_id = sqlc.arg(user_id)
  AND parts.owner_id = sqlc.arg(user_id)
  AND parts.status = 'wanted' AND parts.source_url IS NOT NULL
ORDER BY s.position, s.id;

-- name: RefreshPartPrice :execrows
-- Records a price found on the part's list. A NULL price (the list shows
-- none) keeps the old one. previous_price_cents keeps the old price only
-- when this refresh changed it. Bought parts never change.
-- SET expressions all read the row's old values, so the order is free.
UPDATE parts SET
    price_cents = coalesce(sqlc.narg(price_cents), price_cents),
    previous_price_cents = CASE
        WHEN sqlc.narg(price_cents) IS NOT NULL AND price_cents <> sqlc.narg(price_cents) THEN price_cents
    END,
    price_checked_at = sqlc.arg(checked_at),
    missing_from_source_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(user_id) AND status = 'wanted';

-- name: MarkPartMissingFromSource :execrows
-- The part wasn't on its list at the last refresh. Its price stays as it was.
UPDATE parts SET
    previous_price_cents = NULL,
    missing_from_source_at = sqlc.arg(checked_at),
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(user_id) AND status = 'wanted';
