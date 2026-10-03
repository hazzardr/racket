-- Price sources: a part imported from a PCPartPicker list remembers the
-- list, so a planned build can refresh the prices of parts nobody has
-- bought yet. A bought part keeps the price paid; refreshes skip it.

-- +goose Up
-- The canonical list URL (https://pcpartpicker.com/list/ID), or NULL for
-- parts added by hand or from a CSV upload.
ALTER TABLE parts ADD COLUMN source_url TEXT;
-- When price_cents last came from source_url.
ALTER TABLE parts ADD COLUMN price_checked_at DATETIME;
-- The price before the last refresh changed it; NULL when the last refresh
-- found the same price.
ALTER TABLE parts ADD COLUMN previous_price_cents INTEGER CHECK (previous_price_cents >= 0);
-- Set when the last refresh didn't find the part on its list (it was
-- swapped or removed on PCPartPicker).
ALTER TABLE parts ADD COLUMN missing_from_source_at DATETIME;

-- +goose Down
ALTER TABLE parts DROP COLUMN missing_from_source_at;
ALTER TABLE parts DROP COLUMN previous_price_cents;
ALTER TABLE parts DROP COLUMN price_checked_at;
ALTER TABLE parts DROP COLUMN source_url;
