CREATE TABLE runs (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	topic        TEXT NOT NULL DEFAULT '',
	run          TEXT NOT NULL,
	dir          TEXT NOT NULL UNIQUE,
	question     TEXT NOT NULL DEFAULT '',
	verdict      TEXT NOT NULL DEFAULT '',
	consensus    TEXT NOT NULL DEFAULT '',
	papers       INTEGER NOT NULL DEFAULT 0,
	generated_at TEXT NOT NULL DEFAULT '',
	created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
