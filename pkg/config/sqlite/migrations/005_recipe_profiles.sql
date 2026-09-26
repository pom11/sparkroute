CREATE TABLE gateway_config_managed_sets_profiles (
    owner TEXT PRIMARY KEY,
    revision_id TEXT NOT NULL,
    document_json TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    reason TEXT,
    CHECK (owner IN ('operator', 'sparkrun', 'recipe_profiles')),
    CHECK (length(revision_id) = 64),
    CHECK (length(updated_by) BETWEEN 1 AND 512),
    CHECK (reason IS NULL OR length(reason) <= 4096),
    CHECK (json_valid(document_json))
);
INSERT INTO gateway_config_managed_sets_profiles SELECT * FROM gateway_config_managed_sets;
DROP TABLE gateway_config_managed_sets;
ALTER TABLE gateway_config_managed_sets_profiles RENAME TO gateway_config_managed_sets;
