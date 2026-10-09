-- Welayats and major cities only.
-- IDs are stable so later etrap rows can reference these parents.
-- Ashgabat is a city with no parent: it is administered separately from the five welayats.
-- All rows here are is_active = true. Etraps are not seeded in this file.

INSERT INTO locations (id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order) VALUES
    ('a1000000-0000-4000-8000-000000000001', NULL, 'welayat', 'Ahal', 'Ахал', 'Ahal', true, 1),
    ('a1000000-0000-4000-8000-000000000002', NULL, 'welayat', 'Balkan', 'Балкан', 'Balkan', true, 2),
    ('a1000000-0000-4000-8000-000000000003', NULL, 'welayat', 'Daşoguz', 'Дашогуз', 'Dashoguz', true, 3),
    ('a1000000-0000-4000-8000-000000000004', NULL, 'welayat', 'Lebap', 'Лебап', 'Lebap', true, 4),
    ('a1000000-0000-4000-8000-000000000005', NULL, 'welayat', 'Mary', 'Мары', 'Mary', true, 5);

INSERT INTO locations (id, parent_id, type, name_tk, name_ru, name_en, is_active, sort_order) VALUES
    ('c2000000-0000-4000-8000-000000000001', NULL, 'city', 'Aşgabat', 'Ашхабад', 'Ashgabat', true, 10),
    ('c2000000-0000-4000-8000-000000000002', 'a1000000-0000-4000-8000-000000000001', 'city', 'Änew', 'Энев', 'Anau', true, 11),
    ('c2000000-0000-4000-8000-000000000003', 'a1000000-0000-4000-8000-000000000001', 'city', 'Tejen', 'Теджен', 'Tejen', true, 12),
    ('c2000000-0000-4000-8000-000000000004', 'a1000000-0000-4000-8000-000000000002', 'city', 'Balkanabat', 'Балканабат', 'Balkanabat', true, 13),
    ('c2000000-0000-4000-8000-000000000005', 'a1000000-0000-4000-8000-000000000002', 'city', 'Türkmenbaşy', 'Туркменбаши', 'Turkmenbashi', true, 14),
    ('c2000000-0000-4000-8000-000000000006', 'a1000000-0000-4000-8000-000000000003', 'city', 'Daşoguz', 'Дашогуз', 'Dashoguz', true, 15),
    ('c2000000-0000-4000-8000-000000000007', 'a1000000-0000-4000-8000-000000000004', 'city', 'Türkmenabat', 'Туркменабат', 'Turkmenabat', true, 16),
    ('c2000000-0000-4000-8000-000000000008', 'a1000000-0000-4000-8000-000000000005', 'city', 'Mary', 'Мары', 'Mary', true, 17),
    ('c2000000-0000-4000-8000-000000000009', 'a1000000-0000-4000-8000-000000000005', 'city', 'Baýramaly', 'Байрамалы', 'Bayramaly', true, 18);
