CREATE TABLE shares (
                        code          TEXT PRIMARY KEY,
                        filename      TEXT NOT NULL,
                        spool_path    TEXT,
                        size          INTEGER NOT NULL CHECK (size >= 0),
                        created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
                        expires_at    INTEGER,
                        max_downloads INTEGER,

                        CHECK (expires_at IS NULL OR expires_at > created_at),
                        CHECK (max_downloads IS NULL OR max_downloads >= 0)
);

CREATE TABLE roots (
                       path TEXT PRIMARY KEY
);

CREATE TABLE downloads (
                           code      TEXT NOT NULL,
                           client_ip TEXT NOT NULL,

                           PRIMARY KEY (code, client_ip),
                           FOREIGN KEY (code) REFERENCES shares(code) ON DELETE CASCADE
) WITHOUT ROWID;
