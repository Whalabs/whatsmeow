-- v16 (compatible with v8+): Add broadcast list table synced from app state
CREATE TABLE whatsmeow_broadcast_lists (
	our_jid      TEXT   NOT NULL,
	list_jid     TEXT   NOT NULL,
	name         TEXT   NOT NULL DEFAULT '',
	participants TEXT   NOT NULL DEFAULT '[]',
	label_ids    TEXT   NOT NULL DEFAULT '[]',
	updated_at   BIGINT NOT NULL DEFAULT 0,

	PRIMARY KEY (our_jid, list_jid),
	FOREIGN KEY (our_jid) REFERENCES whatsmeow_device(jid) ON DELETE CASCADE ON UPDATE CASCADE
);
