-- Canonical list URLs: imports stored whatever URL the paste's first line
-- had (a regional subdomain, http, a trailing slash or a query), but price
-- refreshes compare source_url exactly against the canonical
-- https://pcpartpicker.com/list/ID that 20261002000000 promises. Rewrite
-- the stored list URLs to that form. URLs that aren't list URLs stay as
-- they are.

-- +goose Up
-- The host part: any scheme and subdomain becomes https://pcpartpicker.com.
-- lower() only finds the position (it keeps ASCII lengths); the list ID
-- keeps its case.
UPDATE parts
SET source_url = 'https://pcpartpicker.com/list/' ||
    substr(source_url, instr(lower(source_url), 'pcpartpicker.com/list/') + length('pcpartpicker.com/list/'))
WHERE instr(lower(source_url), 'pcpartpicker.com/list/') > 0;

-- Then whatever follows the ID: a fragment, a query, a trailing path.
UPDATE parts SET source_url = substr(source_url, 1, instr(source_url, '#') - 1)
WHERE source_url LIKE 'https://pcpartpicker.com/list/%' AND instr(source_url, '#') > 0;

UPDATE parts SET source_url = substr(source_url, 1, instr(source_url, '?') - 1)
WHERE source_url LIKE 'https://pcpartpicker.com/list/%' AND instr(source_url, '?') > 0;

UPDATE parts
SET source_url = substr(source_url, 1,
    length('https://pcpartpicker.com/list/') + instr(substr(source_url, length('https://pcpartpicker.com/list/') + 1), '/') - 1)
WHERE source_url LIKE 'https://pcpartpicker.com/list/%'
  AND instr(substr(source_url, length('https://pcpartpicker.com/list/') + 1), '/') > 0;

-- +goose Down
-- The original spellings are gone; canonical URLs are valid before this
-- migration too.
