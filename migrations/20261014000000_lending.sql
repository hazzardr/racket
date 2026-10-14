-- Lending: an owner chooses which of their bought parts friends may borrow.
-- Until now every bought spare was borrowable, so the parts that are spare
-- or lent out today start lendable; parts added from now on don't.
--
-- Matching: a planned build "wants" a friend's spare when the part names
-- match, ignoring case and outer spaces. PCPartPicker names a product the
-- same way in every list, so imported parts match reliably. The index
-- serves that lookup (queries must use the same lower(trim(name))).

-- +goose Up
ALTER TABLE parts ADD COLUMN lendable BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE parts SET lendable = TRUE
WHERE status = 'owned' AND (
    slot_id IS NULL
    OR owner_id <> (
        SELECT m.owner_id FROM machine_slots s JOIN machines m ON m.id = s.machine_id
        WHERE s.id = parts.slot_id
    )
);

CREATE INDEX idx_parts_name_key ON parts(lower(trim(name)));

-- +goose Down
DROP INDEX idx_parts_name_key;
ALTER TABLE parts DROP COLUMN lendable;
