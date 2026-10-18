-- When a profile last pointed somewhere new: conversations from before it do not follow it to the new address.
ALTER TABLE provider_profiles ADD COLUMN base_url_changed_at TEXT NOT NULL DEFAULT '';
-- Nothing recorded a move before this column: the last edit of each profile stands in for it.
UPDATE provider_profiles SET base_url_changed_at = updated_at;
