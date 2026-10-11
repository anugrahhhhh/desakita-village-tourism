CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         citext      NOT NULL UNIQUE CHECK (length(email) BETWEEN 3 AND 254),
    password_hash text        NOT NULL,
    name          text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    phone         text,
    is_admin      boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Session store (layout expected by alexedwards/scs pgxstore).
CREATE TABLE sessions (
    token  text        PRIMARY KEY,
    data   bytea       NOT NULL,
    expiry timestamptz NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- Generic administrative hierarchy; no region data is seeded by migrations.
CREATE TABLE regions (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    parent_id bigint REFERENCES regions (id) ON DELETE RESTRICT,
    kind      text   NOT NULL CHECK (kind IN ('province', 'regency', 'district')),
    name      text   NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    CHECK ((kind = 'province') = (parent_id IS NULL))
);
CREATE UNIQUE INDEX regions_parent_name_uniq ON regions (coalesce(parent_id, 0), name);
CREATE INDEX regions_parent_idx ON regions (parent_id);

CREATE TABLE activity_types (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 60)
);

CREATE TABLE villages (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug             text        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name             text        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description      text        NOT NULL DEFAULT '' CHECK (length(description) <= 5000),
    district_id      bigint      NOT NULL REFERENCES regions (id) ON DELETE RESTRICT,
    address          text        NOT NULL DEFAULT '' CHECK (length(address) <= 300),
    lat              double precision CHECK (lat BETWEEN -90 AND 90),
    lng              double precision CHECK (lng BETWEEN -180 AND 180),
    contact_phone    text        NOT NULL,
    contact_whatsapp text,
    status           text        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'verified', 'rejected', 'suspended')),
    review_note      text        NOT NULL DEFAULT '' CHECK (length(review_note) <= 1000),
    verified_by      bigint      REFERENCES users (id) ON DELETE SET NULL,
    verified_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((lat IS NULL) = (lng IS NULL))
);
CREATE INDEX villages_status_district_idx ON villages (status, district_id);

CREATE TABLE village_members (
    village_id bigint      NOT NULL REFERENCES villages (id) ON DELETE CASCADE,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text        NOT NULL DEFAULT 'owner' CHECK (role IN ('owner')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (village_id, user_id)
);
CREATE INDEX village_members_user_idx ON village_members (user_id);

CREATE TABLE tour_packages (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    village_id           bigint      NOT NULL REFERENCES villages (id) ON DELETE RESTRICT,
    name                 text        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description          text        NOT NULL DEFAULT '' CHECK (length(description) <= 5000),
    price_per_person_idr bigint      NOT NULL CHECK (price_per_person_idr >= 0),
    duration_days        smallint    NOT NULL CHECK (duration_days >= 1),
    duration_nights      smallint    NOT NULL DEFAULT 0
                         CHECK (duration_nights >= 0 AND duration_nights <= duration_days),
    default_quota        integer     NOT NULL CHECK (default_quota > 0),
    is_active            boolean     NOT NULL DEFAULT true,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    -- Lets bookings reference (package_id, village_id) so the denormalized village_id can't drift.
    UNIQUE (id, village_id)
);
CREATE INDEX tour_packages_village_idx ON tour_packages (village_id);

CREATE TABLE package_activity_types (
    package_id       bigint NOT NULL REFERENCES tour_packages (id) ON DELETE CASCADE,
    activity_type_id bigint NOT NULL REFERENCES activity_types (id) ON DELETE RESTRICT,
    PRIMARY KEY (package_id, activity_type_id)
);
CREATE INDEX package_activity_types_type_idx ON package_activity_types (activity_type_id);

CREATE TABLE media (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    village_id        bigint      REFERENCES villages (id) ON DELETE CASCADE,
    package_id        bigint      REFERENCES tour_packages (id) ON DELETE CASCADE,
    path              text        NOT NULL UNIQUE,
    mime              text        NOT NULL CHECK (mime IN ('image/jpeg', 'image/png', 'image/webp')),
    bytes             integer     NOT NULL CHECK (bytes > 0),
    sort_order        integer     NOT NULL DEFAULT 0,
    consent_confirmed boolean     NOT NULL CHECK (consent_confirmed),
    uploaded_by       bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(village_id, package_id) = 1)
);
CREATE INDEX media_village_idx ON media (village_id) WHERE village_id IS NOT NULL;
CREATE INDEX media_package_idx ON media (package_id) WHERE package_id IS NOT NULL;

-- Per departure date. Rows are created lazily; a missing row means "open at default_quota".
CREATE TABLE availability (
    package_id bigint      NOT NULL REFERENCES tour_packages (id) ON DELETE RESTRICT,
    date       date        NOT NULL,
    quota      integer     NOT NULL CHECK (quota >= 0),
    reserved   integer     NOT NULL DEFAULT 0,
    is_closed  boolean     NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (package_id, date),
    CHECK (reserved >= 0 AND reserved <= quota)
);

CREATE TABLE bookings (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    package_id      bigint      NOT NULL,
    village_id      bigint      NOT NULL,
    traveler_id     bigint      REFERENCES users (id) ON DELETE SET NULL,
    start_date      date        NOT NULL,
    party_size      integer     NOT NULL CHECK (party_size > 0),
    note            text        NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
    total_price_idr bigint      NOT NULL CHECK (total_price_idr >= 0),
    contact_name    text        NOT NULL CHECK (length(contact_name) <= 100),
    contact_phone   text        NOT NULL CHECK (length(contact_phone) <= 20),
    status          text        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'confirmed', 'rejected', 'cancelled', 'expired', 'completed')),
    expires_at      timestamptz NOT NULL,
    decided_at      timestamptz,
    completed_at    timestamptz,
    cancelled_at    timestamptz,
    cancelled_by    text        CHECK (cancelled_by IN ('traveler', 'manager')),
    decision_note   text        NOT NULL DEFAULT '' CHECK (length(decision_note) <= 500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (package_id, village_id) REFERENCES tour_packages (id, village_id) ON DELETE RESTRICT,
    CHECK ((status = 'cancelled') = (cancelled_by IS NOT NULL AND cancelled_at IS NOT NULL)),
    CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);
CREATE INDEX bookings_village_status_date_idx ON bookings (village_id, status, start_date);
CREATE INDEX bookings_traveler_status_idx ON bookings (traveler_id, status);
CREATE INDEX bookings_package_date_idx ON bookings (package_id, start_date);
CREATE INDEX bookings_pending_expiry_idx ON bookings (expires_at) WHERE status = 'pending';

CREATE TABLE reports (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reporter_id bigint      REFERENCES users (id) ON DELETE SET NULL,
    target_type text        NOT NULL CHECK (target_type IN ('village', 'package')),
    target_id   bigint      NOT NULL,
    reason      text        NOT NULL CHECK (length(reason) BETWEEN 1 AND 1000),
    status      text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by bigint      REFERENCES users (id) ON DELETE SET NULL,
    resolved_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reports_status_idx ON reports (status, created_at);
