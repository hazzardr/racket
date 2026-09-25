-- DRAFT: replacement schema for racket (multi-user, racks, build planning).
--
-- Not wired up yet. Once reviewed, this replaces both files in migrations/
-- as a single squashed initial migration (the existing database has no data).
--
-- Model:
--   users ─┬─< sessions
--          ├─< machines ─< machine_slots ─o parts   (a slot holds at most one part)
--          ├─< parts                                 (inventory; slot_id NULL = unassigned;
--          │                                          may sit in another user's slot)
--          └─< racks ─< rack_mounts >o machines      (mount is a machine or a labeled device)
--
-- Access model (enforced in queries, not schema): login is limited to members
-- of one Discord server; any signed-in user can read everything; only the
-- owner can write. Every write query filters on owner_id.
--
-- Borrowing: a machine owner may put another user's part in their slot, if
-- that part is 'owned' and not already in a slot. The part keeps its owner.
-- Either the machine owner or the part owner can take it out again. The UI
-- tells parts apart relative to the machine's owner:
--   status = 'wanted'                  -> not purchased
--   parts.owner_id = machines.owner_id -> the builder's own part
--   otherwise                          -> borrowed from parts.owner_id
--   ...whose owner has left            -> from a former member
--
-- Leaving: removing a user sets users.deleted_at (soft delete) and ends their
-- sessions. Parts they lent stay in friends' machines; everything else of
-- theirs drops out of listings and can't be borrowed or edited. Logging in
-- again (still gated on the Discord server) clears deleted_at and restores it
-- all. Deleting the users row is the hard purge: the cascades below remove
-- everything they own, including lent parts.

-- +goose Up

-- Users are created on first Discord login; there are no passwords.
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    discord_id TEXT NOT NULL UNIQUE,  -- snowflake, kept as text
    username TEXT NOT NULL,           -- Discord handle, refreshed each login
    display_name TEXT,                -- Discord global_name
    avatar_hash TEXT,                 -- builds cdn.discordapp.com avatar URLs
    is_admin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at DATETIME,
    deleted_at DATETIME               -- set when the user leaves; see header
);

-- The cookie holds a random token; only its SHA-256 is stored, so a leaked
-- database can't be replayed as live sessions.
CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

-- A machine is a PC, server, NAS, switch, etc. "planned" covers builds that
-- don't exist yet; the parts in a planned build can be "wanted" parts.
CREATE TABLE machines (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'desktop'
        CHECK (kind IN ('desktop', 'server', 'nas', 'network', 'laptop', 'other')),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('planned', 'active', 'retired')),
    form_factor TEXT,                 -- free text: "ATX mid tower", "2U", "SFF"
    description TEXT,
    location TEXT,
    built_on DATE,
    purchased_on DATE,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (owner_id, name)
);

-- A slot is a place in a machine's build sheet: "CPU", "RAM", "Storage".
-- position orders the slots on the page.
CREATE TABLE machine_slots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id INTEGER NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_machine_slots_machine_id ON machine_slots(machine_id, position);

-- A part is one physical (or wanted) item in a user's inventory. It is in a
-- machine only through a slot, so there is one source of truth for
-- "where is this part". Deleting a slot or machine returns its parts to
-- their owner's inventory instead of deleting them. For a 'wanted' part,
-- owner_id is whoever added it to a plan; nobody has bought it yet.
CREATE TABLE parts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slot_id INTEGER REFERENCES machine_slots(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'owned'
        CHECK (status IN ('wanted', 'owned', 'retired')),
    name TEXT NOT NULL,
    category TEXT,
    manufacturer TEXT,
    model TEXT,
    serial_number TEXT,
    price_cents INTEGER CHECK (price_cents >= 0),
    currency TEXT NOT NULL DEFAULT 'USD',
    purchased_on DATE,
    purchased_from TEXT,
    warranty_expires DATE,
    product_url TEXT,
    guide_url TEXT,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (status <> 'retired' OR slot_id IS NULL)  -- retired parts aren't installed
);
CREATE INDEX idx_parts_owner_id ON parts(owner_id, status);
CREATE UNIQUE INDEX idx_parts_slot_id ON parts(slot_id) WHERE slot_id IS NOT NULL;

CREATE TABLE racks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    height_u INTEGER NOT NULL DEFAULT 42 CHECK (height_u BETWEEN 1 AND 60),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('planned', 'active', 'retired')),
    location TEXT,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (owner_id, name)
);

-- One occupied span in a rack. It is either a tracked machine or a labeled
-- device that isn't worth tracking (patch panel, PDU, shelf, blanking plate).
-- position_u is the lowest U the item occupies, counted from 1 at the bottom.
-- The app checks that spans fit in the rack and don't overlap on the same
-- face; half-depth gear on opposite faces may share U.
CREATE TABLE rack_mounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rack_id INTEGER NOT NULL REFERENCES racks(id) ON DELETE CASCADE,
    machine_id INTEGER UNIQUE REFERENCES machines(id) ON DELETE CASCADE,
    label TEXT,
    position_u INTEGER NOT NULL CHECK (position_u >= 1),
    height_u INTEGER NOT NULL DEFAULT 1 CHECK (height_u >= 1),
    face TEXT NOT NULL DEFAULT 'front' CHECK (face IN ('front', 'rear')),
    full_depth BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((machine_id IS NULL) <> (label IS NULL))
);
CREATE INDEX idx_rack_mounts_rack_id ON rack_mounts(rack_id, position_u);

-- +goose Down
DROP TABLE IF EXISTS rack_mounts;
DROP TABLE IF EXISTS racks;
DROP TABLE IF EXISTS parts;
DROP TABLE IF EXISTS machine_slots;
DROP TABLE IF EXISTS machines;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
