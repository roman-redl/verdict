CREATE TABLE jobs (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	status     TEXT NOT NULL,
	question   TEXT NOT NULL DEFAULT '',
	topic      TEXT NOT NULL DEFAULT '',
	dir        TEXT NOT NULL DEFAULT '',
	verdict    TEXT NOT NULL DEFAULT '',
	error      TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX jobs_status ON jobs (status);
