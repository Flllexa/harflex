-- When a profile last pointed somewhere new: conversations from before it do not follow it to the new address.
ALTER TABLE provider_profiles ADD COLUMN base_url_changed_at TEXT NOT NULL DEFAULT '';
