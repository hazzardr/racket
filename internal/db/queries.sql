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
    warranty_expires, purchased_on, purchased_from, purchase_price, guide_url
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdatePart :one
UPDATE parts
SET machine_id = ?, name = ?, category = ?, description = ?, manufacturer = ?, model = ?,
    serial_number = ?, warranty_expires = ?, purchased_on = ?, purchased_from = ?,
    purchase_price = ?, guide_url = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeletePart :exec
DELETE FROM parts
WHERE id = ?;
