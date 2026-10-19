CREATE TABLE orgs (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE repos (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     BIGINT NOT NULL REFERENCES orgs (id),
    full_name  TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE reviews (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo_id    BIGINT NOT NULL REFERENCES repos (id),
    pr_number  INT NOT NULL,
    head_sha   TEXT NOT NULL,
    status     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reviews_repo_id_idx ON reviews (repo_id);

CREATE TABLE findings (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    review_id  BIGINT NOT NULL REFERENCES reviews (id),
    rule_id    TEXT NOT NULL,
    path       TEXT NOT NULL,
    line       INT NOT NULL,
    severity   TEXT NOT NULL,
    evidence   JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX findings_review_id_idx ON findings (review_id);

CREATE TABLE feedback (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    finding_id BIGINT NOT NULL REFERENCES findings (id),
    vote       SMALLINT NOT NULL CHECK (vote IN (-1, 1)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX feedback_finding_id_idx ON feedback (finding_id);
