-- Older local catalogs may have recorded migration versions 036/037 without
-- the execution baseline columns. The SQLite migrator checks each column and
-- adds only missing fields in this transaction, preserving existing evidence.
SELECT 1;
