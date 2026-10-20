\set ON_ERROR_STOP on

BEGIN;
SELECT 'CREATE ROLE endurain_exporter LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 3'
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'endurain_exporter')
\gexec

ALTER ROLE endurain_exporter NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 3;
ALTER ROLE endurain_exporter SET default_transaction_read_only = on;
ALTER ROLE endurain_exporter SET statement_timeout = '5s';
ALTER ROLE endurain_exporter SET lock_timeout = '1s';
ALTER ROLE endurain_exporter SET idle_in_transaction_session_timeout = '10s';

SELECT format('GRANT CONNECT ON DATABASE %I TO endurain_exporter', current_database())
\gexec
GRANT USAGE ON SCHEMA public TO endurain_exporter;
GRANT SELECT (id) ON public.users TO endurain_exporter;
GRANT SELECT (user_id, activity_type, distance, total_timer_time, elevation_gain, calories, start_time, is_hidden)
ON public.activities TO endurain_exporter;
COMMIT;
