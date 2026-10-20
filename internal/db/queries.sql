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

-- name: FindCurrentUsers :many
-- Members who haven't left, by username or Discord ID, for removing one.
-- Usernames aren't unique, so this can find more than one.
SELECT * FROM users
WHERE deleted_at IS NULL AND (username = sqlc.arg(who) OR discord_id = sqlc.arg(who))
ORDER BY id;

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

-- name: ListMembers :many
-- Members who haven't left, with how many machines they have and how many
-- spares they lend, for the people page.
SELECT sqlc.embed(users),
    (SELECT count(*) FROM machines m WHERE m.owner_id = users.id) AS machine_count,
    (SELECT count(*) FROM parts p
     WHERE p.owner_id = users.id AND p.status = 'owned' AND p.lendable AND p.slot_id IS NULL) AS lending_count
FROM users
WHERE users.deleted_at IS NULL
ORDER BY lower(coalesce(users.display_name, users.username)), users.id;

-- name: GetMember :one
-- A member who hasn't left.
SELECT * FROM users WHERE id = ? AND deleted_at IS NULL;

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
-- Appends a slot after the machine's existing ones. It inserts nothing
-- (sql.ErrNoRows) unless the user owns the machine.
INSERT INTO machine_slots (machine_id, category, position)
SELECT
    sqlc.arg(machine_id), sqlc.arg(category),
    (SELECT coalesce(max(position), -1) + 1 FROM machine_slots WHERE machine_id = sqlc.arg(machine_id))
WHERE EXISTS (SELECT 1 FROM machines WHERE id = sqlc.arg(machine_id) AND owner_id = sqlc.arg(user_id))
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
       parts.lendable AS part_lendable,
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

-- name: ListSlotStates :many
-- Every slot of every machine whose owner hasn't left, with part_source as
-- ListSlotsByMachine labels it, for the machine list's slot LEDs.
SELECT machine_slots.machine_id, machine_slots.category,
       CASE
         WHEN parts.id IS NULL THEN 'empty'
         WHEN parts.status = 'wanted' THEN 'unpurchased'
         WHEN parts.owner_id = machines.owner_id THEN 'own'
         WHEN users.deleted_at IS NOT NULL THEN 'former_member'
         ELSE 'borrowed'
       END AS part_source
FROM machine_slots
JOIN machines ON machines.id = machine_slots.machine_id
JOIN users AS owners ON owners.id = machines.owner_id
LEFT JOIN parts ON parts.slot_id = machine_slots.id
LEFT JOIN users ON users.id = parts.owner_id
WHERE owners.deleted_at IS NULL
ORDER BY machine_slots.machine_id, machine_slots.position, machine_slots.id;

-- Parts ---------------------------------------------------------------------

-- name: CreatePart :one
-- A part in a slot goes only into one of its owner's machines: otherwise
-- it inserts nothing (sql.ErrNoRows).
INSERT INTO parts (
    owner_id, slot_id, status, name, category, manufacturer, model,
    price_cents, purchased_from, source_url, price_checked_at, lendable
)
SELECT
    sqlc.arg(owner_id), sqlc.narg(slot_id), sqlc.arg(status), sqlc.arg(name),
    sqlc.narg(category), sqlc.narg(manufacturer), sqlc.narg(model),
    sqlc.narg(price_cents), sqlc.narg(purchased_from), sqlc.narg(source_url),
    sqlc.narg(price_checked_at), sqlc.arg(lendable)
WHERE sqlc.narg(slot_id) IS NULL OR EXISTS (
    SELECT 1 FROM machine_slots s JOIN machines m ON m.id = s.machine_id
    WHERE s.id = sqlc.narg(slot_id) AND m.owner_id = sqlc.arg(owner_id)
)
RETURNING *;

-- name: ListPartsByOwner :many
SELECT * FROM parts WHERE owner_id = ? ORDER BY created_at DESC, id DESC;

-- name: ListSpareParts :many
-- Parts in no slot that the user could put in one of their machines: their
-- own (bought or not), and the bought parts members who haven't left lend.
SELECT parts.id, parts.name, parts.category, parts.status, parts.price_cents,
       parts.owner_id, users.username AS owner_name
FROM parts
JOIN users ON users.id = parts.owner_id
WHERE parts.slot_id IS NULL AND parts.status <> 'retired'
  AND users.deleted_at IS NULL
  AND (parts.owner_id = sqlc.arg(user_id) OR (parts.status = 'owned' AND parts.lendable))
ORDER BY users.username, parts.name, parts.id;

-- name: AssignPartToSlot :execrows
-- The caller must own the slot's machine. Their own parts can move freely;
-- another user's part must be owned (bought), lendable, not already in a
-- slot, and belong to someone who hasn't left.
UPDATE parts SET slot_id = sqlc.arg(slot_id), updated_at = CURRENT_TIMESTAMP
WHERE parts.id = sqlc.arg(part_id)
  AND EXISTS (
    SELECT 1 FROM machine_slots s JOIN machines m ON m.id = s.machine_id
    WHERE s.id = sqlc.arg(slot_id) AND m.owner_id = sqlc.arg(user_id)
  )
  AND (
    parts.owner_id = sqlc.arg(user_id)
    OR (
      parts.status = 'owned' AND parts.lendable AND parts.slot_id IS NULL
      AND EXISTS (SELECT 1 FROM users u WHERE u.id = parts.owner_id AND u.deleted_at IS NULL)
    )
  );

-- name: UnassignPart :execrows
-- Either the part's owner or the owner of the machine it sits in. With a
-- slot_id, only while the part is still in that slot.
UPDATE parts SET slot_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE parts.id = sqlc.arg(part_id)
  AND (parts.slot_id = sqlc.narg(slot_id) OR sqlc.narg(slot_id) IS NULL)
  AND (parts.owner_id = sqlc.arg(user_id) OR parts.slot_id IN (
    SELECT s.id FROM machine_slots s JOIN machines m ON m.id = s.machine_id
    WHERE m.owner_id = sqlc.arg(user_id)
  ));

-- name: DeletePart :execrows
-- Only while the part is still in the slot the caller saw it in.
DELETE FROM parts WHERE id = ? AND owner_id = ? AND slot_id = ?;

-- name: DeleteSparePart :execrows
-- Only a part in no slot, so deleting never empties a friend's machine.
DELETE FROM parts WHERE id = ? AND owner_id = ? AND slot_id IS NULL;

-- name: SetPartLendable :execrows
-- Only bought parts can be lent. A part already on loan stays where it is.
UPDATE parts SET lendable = sqlc.arg(lendable), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(user_id) AND status = 'owned';

-- Lending -------------------------------------------------------------------

-- name: ListSparesByOwner :many
-- The user's parts in no slot, bought or not, for the parts page.
SELECT * FROM parts
WHERE owner_id = ? AND slot_id IS NULL AND status <> 'retired'
ORDER BY lower(coalesce(category, '')), lower(name), id;

-- name: ListSpareDemand :many
-- Who wants the user's bought spares: members who haven't left with a
-- not-yet-bought part of the same name in one of their machines. A machine
-- that wants several (two RAM slots) is listed once.
SELECT DISTINCT spare.id AS part_id, users.id AS user_id, users.username,
       machines.id AS machine_id, machines.name AS machine_name
FROM parts spare
JOIN parts want ON lower(trim(want.name)) = lower(trim(spare.name))
  AND want.status = 'wanted'
JOIN machine_slots s ON s.id = want.slot_id
JOIN machines ON machines.id = s.machine_id
JOIN users ON users.id = machines.owner_id
WHERE spare.owner_id = sqlc.arg(user_id) AND spare.slot_id IS NULL AND spare.status = 'owned'
  AND machines.owner_id <> sqlc.arg(user_id) AND users.deleted_at IS NULL
ORDER BY spare.id, users.username, machines.name;

-- name: ListSlotSpareMatches :many
-- Spares that could stand in for the machine's not-yet-bought parts: the
-- user's own bought spares and the spares members who haven't left lend,
-- with the same name. Own spares come first.
SELECT want.slot_id, spare.id AS part_id, spare.owner_id, users.username AS owner_name
FROM parts want
JOIN machine_slots s ON s.id = want.slot_id
JOIN machines m ON m.id = s.machine_id
JOIN parts spare ON lower(trim(spare.name)) = lower(trim(want.name))
  AND spare.status = 'owned' AND spare.slot_id IS NULL
JOIN users ON users.id = spare.owner_id AND users.deleted_at IS NULL
WHERE s.machine_id = sqlc.arg(machine_id) AND want.status = 'wanted'
  AND (spare.owner_id = sqlc.arg(user_id) OR spare.lendable)
ORDER BY s.position, s.id, spare.owner_id <> m.owner_id, lower(users.username), spare.id;

-- name: ListLendingSpares :many
-- The spares a user lends, for their profile.
SELECT * FROM parts
WHERE owner_id = ? AND status = 'owned' AND lendable AND slot_id IS NULL
ORDER BY lower(coalesce(category, '')), lower(name), id;

-- name: ListFriendSpares :many
-- The spares other members who haven't left lend, for the parts page's
-- friends' view.
SELECT sqlc.embed(parts), users.username AS owner_name
FROM parts
JOIN users ON users.id = parts.owner_id
WHERE parts.owner_id <> sqlc.arg(user_id) AND users.deleted_at IS NULL
  AND parts.status = 'owned' AND parts.lendable AND parts.slot_id IS NULL
ORDER BY lower(coalesce(parts.category, '')), lower(parts.name), lower(users.username), parts.id;

-- name: GetFriendSpare :one
-- One of the spares ListFriendSpares lists, for borrowing it.
SELECT sqlc.embed(parts), users.username AS owner_name
FROM parts
JOIN users ON users.id = parts.owner_id
WHERE parts.id = sqlc.arg(id)
  AND parts.owner_id <> sqlc.arg(user_id) AND users.deleted_at IS NULL
  AND parts.status = 'owned' AND parts.lendable AND parts.slot_id IS NULL;

-- name: ListBorrowTargets :many
-- The slots in the user's machines a borrowed part could go in: empty
-- ones, and ones holding the user's own part not bought yet (wanted_name).
SELECT s.id AS slot_id, s.category, m.id AS machine_id, m.name AS machine_name,
       p.name AS wanted_name
FROM machine_slots s
JOIN machines m ON m.id = s.machine_id
LEFT JOIN parts p ON p.slot_id = s.id
WHERE m.owner_id = sqlc.arg(user_id)
  AND (p.id IS NULL OR (p.status = 'wanted' AND p.owner_id = m.owner_id))
ORDER BY lower(m.name), m.id, s.position, s.id;

-- name: ListLentParts :many
-- The user's parts in other users' machines, including machines of members
-- who have left (the lender can still take those parts back).
SELECT parts.id, parts.name, parts.category,
       machines.id AS machine_id, machines.name AS machine_name,
       users.id AS borrower_id, users.username AS borrower_name, users.deleted_at AS borrower_left_at
FROM parts
JOIN machine_slots s ON s.id = parts.slot_id
JOIN machines ON machines.id = s.machine_id
JOIN users ON users.id = machines.owner_id
WHERE parts.owner_id = sqlc.arg(user_id) AND machines.owner_id <> sqlc.arg(user_id)
ORDER BY lower(users.username), lower(parts.name), parts.id;

-- name: ListBorrowedParts :many
-- Other users' parts in the user's machines, including parts lent by
-- members who have left.
SELECT parts.id, parts.name, parts.category,
       machines.id AS machine_id, machines.name AS machine_name,
       users.id AS lender_id, users.username AS lender_name, users.deleted_at AS lender_left_at
FROM parts
JOIN machine_slots s ON s.id = parts.slot_id
JOIN machines ON machines.id = s.machine_id
JOIN users ON users.id = parts.owner_id
WHERE machines.owner_id = sqlc.arg(user_id) AND parts.owner_id <> sqlc.arg(user_id)
ORDER BY lower(machines.name), s.position, s.id;

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
