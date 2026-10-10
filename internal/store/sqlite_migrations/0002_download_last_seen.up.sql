ALTER TABLE downloads ADD COLUMN last_seen_at INTEGER;

-- existing rows have no history; treat them as seen now so the grace period starts fresh
UPDATE downloads SET last_seen_at = unixepoch();
