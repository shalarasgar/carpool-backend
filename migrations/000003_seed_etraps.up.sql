-- Official etrap seed.
--
-- Add etrap (district) rows here only from an official, up-to-date list.
-- Do not invent names, Russian/Turkmen spellings, or parent welayats.
--
-- Expected shape, once a verified list exists:
--   INSERT INTO locations (parent_id, type, name_tk, name_ru, name_en, is_active, sort_order)
--   VALUES ('<welayat uuid>', 'etrap', '...', '...', '...', false, ...);
--
-- Keep is_active = false until etrap trips are enabled. Phase 1 does not read this file.
-- Welayat parent ids are declared in 000002_seed_locations.up.sql.

-- No-op until official etrap rows are added above.
SELECT 1;
