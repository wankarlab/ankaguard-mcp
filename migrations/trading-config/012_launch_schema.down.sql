-- Reverts 012_launch_schema.up.sql. Destroys all AnkaLaunch data.
BEGIN;
DROP SCHEMA IF EXISTS launch CASCADE;
COMMIT;
