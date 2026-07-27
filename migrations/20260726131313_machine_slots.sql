-- +goose Up
CREATE TABLE machine_slots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id INTEGER NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_machine_slots_machine_id ON machine_slots(machine_id);

ALTER TABLE parts ADD COLUMN slot_id INTEGER REFERENCES machine_slots(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX idx_parts_slot_id ON parts(slot_id) WHERE slot_id IS NOT NULL;

-- backfill standard slots for existing machines
INSERT INTO machine_slots (machine_id, category)
SELECT m.id, c.category FROM machines m CROSS JOIN (
    SELECT 'Motherboard' AS category UNION ALL SELECT 'CPU' UNION ALL
    SELECT 'RAM' UNION ALL SELECT 'RAM' UNION ALL
    SELECT 'Storage' UNION ALL SELECT 'Storage' UNION ALL
    SELECT 'GPU' UNION ALL SELECT 'PSU' UNION ALL SELECT 'Case'
) c;

-- +goose Down
DROP INDEX IF EXISTS idx_parts_slot_id;
ALTER TABLE parts DROP COLUMN slot_id;
DROP INDEX IF EXISTS idx_machine_slots_machine_id;
DROP TABLE IF EXISTS machine_slots;
