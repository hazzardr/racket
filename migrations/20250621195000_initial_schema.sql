-- +goose Up
CREATE TABLE IF NOT EXISTS machines (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    location TEXT,
    built_on DATETIME,
    purchased_on DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS parts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id INTEGER REFERENCES machines(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    category TEXT,
    description TEXT,
    manufacturer TEXT,
    model TEXT,
    serial_number TEXT,
    warranty_expires DATETIME,
    purchased_on DATETIME,
    purchased_from TEXT,
    purchase_price REAL,
    guide_url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_parts_machine_id ON parts(machine_id);

-- +goose Down
DROP INDEX IF EXISTS idx_parts_machine_id;
DROP TABLE IF EXISTS parts;
DROP TABLE IF EXISTS machines;
