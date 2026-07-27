-- name: ListMachines :many
SELECT * FROM machines
ORDER BY created_at DESC;

-- name: GetMachine :one
SELECT * FROM machines
WHERE id = ?;

-- name: CreateMachine :one
INSERT INTO machines (name, description, location, built_on, purchased_on)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateMachine :one
UPDATE machines
SET name = ?, description = ?, location = ?, built_on = ?, purchased_on = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteMachine :exec
DELETE FROM machines
WHERE id = ?;

-- name: ListParts :many
SELECT * FROM parts
ORDER BY created_at DESC;

-- name: ListPartsByMachine :many
SELECT * FROM parts
WHERE machine_id = ?
ORDER BY created_at DESC;

-- name: GetPart :one
SELECT * FROM parts
WHERE id = ?;

-- name: CreatePart :one
INSERT INTO parts (
    machine_id, name, category, description, manufacturer, model, serial_number,
    warranty_expires, purchased_on, purchased_from, purchase_price, guide_url, slot_id
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdatePart :one
UPDATE parts
SET machine_id = ?, name = ?, category = ?, description = ?, manufacturer = ?, model = ?,
    serial_number = ?, warranty_expires = ?, purchased_on = ?, purchased_from = ?,
    purchase_price = ?, guide_url = ?, slot_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: CreateSlot :one
INSERT INTO machine_slots (machine_id, category) VALUES (?, ?) RETURNING *;

-- name: ListSlotsByMachine :many
SELECT machine_slots.*, parts.id AS part_id, parts.name AS part_name, parts.model AS part_model
FROM machine_slots
LEFT JOIN parts ON parts.slot_id = machine_slots.id
WHERE machine_slots.machine_id = ?
ORDER BY machine_slots.id;

-- name: GetSlot :one
SELECT * FROM machine_slots WHERE id = ?;

-- name: DeletePart :exec
DELETE FROM parts
WHERE id = ?;

-- name: DeleteSlot :exec
DELETE FROM machine_slots WHERE id = ?;

-- name: UnassignPartsBySlot :exec
UPDATE parts SET slot_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE slot_id = ?;
