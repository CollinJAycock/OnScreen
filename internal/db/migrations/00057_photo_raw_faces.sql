-- +goose Up
-- +goose StatementBegin
-- Photo libraries: camera RAW pairing, faces and people.
--
-- library_photo_settings holds per-library photo options. No row means the
-- defaults, so existing photo libraries need no backfill.
--
--   raw_jpeg_pairing  a RAW and a JPEG (or HEIC / PNG / ...) with the same
--                     name in the same folder are one photo: the JPEG is
--                     shown, the RAW is a second file of the same item and
--                     is what "Download original" serves. Default on.
CREATE TABLE public.library_photo_settings (
    library_id       uuid PRIMARY KEY REFERENCES public.libraries(id) ON DELETE CASCADE,
    raw_jpeg_pairing boolean DEFAULT true NOT NULL,
    updated_at       timestamp with time zone DEFAULT now() NOT NULL
);

-- People named in a photo library. Names are per library (a person in the
-- family library is not the same row as one in another library) and unique
-- within it, case-insensitively.
CREATE TABLE public.photo_people (
    id           uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    library_id   uuid NOT NULL REFERENCES public.libraries(id) ON DELETE CASCADE,
    name         text NOT NULL,
    created_by   uuid REFERENCES public.users(id) ON DELETE SET NULL,
    created_at   timestamp with time zone DEFAULT now() NOT NULL,
    updated_at   timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT photo_people_name_check CHECK (char_length(name) BETWEEN 1 AND 100)
);
CREATE UNIQUE INDEX idx_photo_people_library_name ON public.photo_people (library_id, lower(name));

-- One detected (or hand-drawn) face on a photo. The box is normalised to
-- 0..1 of the upright image (EXIF orientation applied), so clients draw it
-- over any size of the displayed photo. library_id is denormalised from the
-- item so per-library listings and "delete all face data" don't join
-- media_items.
CREATE TABLE public.photo_faces (
    id          uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    item_id     uuid NOT NULL REFERENCES public.media_items(id) ON DELETE CASCADE,
    library_id  uuid NOT NULL REFERENCES public.libraries(id) ON DELETE CASCADE,
    person_id   uuid REFERENCES public.photo_people(id) ON DELETE SET NULL,
    x           real NOT NULL,
    y           real NOT NULL,
    w           real NOT NULL,
    h           real NOT NULL,
    score       real NOT NULL DEFAULT 0,
    source      text NOT NULL DEFAULT 'auto',
    created_at  timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT photo_faces_box_check CHECK (x >= 0 AND y >= 0 AND w > 0 AND h > 0 AND x + w <= 1.0001 AND y + h <= 1.0001),
    CONSTRAINT photo_faces_source_check CHECK (source = ANY (ARRAY['auto'::text, 'manual'::text]))
);
CREATE INDEX idx_photo_faces_item ON public.photo_faces (item_id);
CREATE INDEX idx_photo_faces_person ON public.photo_faces (person_id) WHERE person_id IS NOT NULL;
CREATE INDEX idx_photo_faces_library_unnamed ON public.photo_faces (library_id) WHERE person_id IS NULL;

-- Which photos the face-detection task has looked at, so each run picks up
-- where the last stopped. A row with faces = 0 is a photo with no faces.
CREATE TABLE public.photo_face_scans (
    item_id     uuid PRIMARY KEY REFERENCES public.media_items(id) ON DELETE CASCADE,
    library_id  uuid NOT NULL REFERENCES public.libraries(id) ON DELETE CASCADE,
    faces       integer NOT NULL DEFAULT 0,
    status      text NOT NULL DEFAULT 'ok',
    scanned_at  timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT photo_face_scans_status_check CHECK (status = ANY (ARRAY['ok'::text, 'failed'::text]))
);
CREATE INDEX idx_photo_face_scans_library ON public.photo_face_scans (library_id);

-- Photos are identified by their file, not their title. The title unique
-- key made every IMG_0001.JPG in a library — one per camera roll-over, one
-- per folder — the same photo, and left no way to keep a RAW and its JPEG
-- apart when pairing is off. The scanner now resolves photo items by path
-- (and pairs by folder + name), so photos leave the title key.
DROP INDEX IF EXISTS public.idx_media_items_library_type_title_year;
CREATE UNIQUE INDEX idx_media_items_library_type_title_year ON public.media_items USING btree (library_id, type, title, COALESCE(year, 0)) WHERE ((parent_id IS NULL) AND (deleted_at IS NULL) AND (type <> 'photo'::text));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_media_items_library_type_title_year;
-- Restore the title key for photos too. Photos that now share a title (two
-- folders' IMG_0001, an unpaired RAW and its JPEG) would make that fail, so
-- fall back to the photo-free key rather than leave the database half
-- rolled back.
DO $$
BEGIN
    CREATE UNIQUE INDEX idx_media_items_library_type_title_year ON public.media_items USING btree (library_id, type, title, COALESCE(year, 0)) WHERE ((parent_id IS NULL) AND (deleted_at IS NULL));
EXCEPTION WHEN unique_violation THEN
    RAISE NOTICE 'photos share titles; keeping photos out of idx_media_items_library_type_title_year';
    CREATE UNIQUE INDEX idx_media_items_library_type_title_year ON public.media_items USING btree (library_id, type, title, COALESCE(year, 0)) WHERE ((parent_id IS NULL) AND (deleted_at IS NULL) AND (type <> 'photo'::text));
END $$;
DROP TABLE IF EXISTS public.photo_face_scans;
DROP TABLE IF EXISTS public.photo_faces;
DROP TABLE IF EXISTS public.photo_people;
DROP TABLE IF EXISTS public.library_photo_settings;
-- +goose StatementEnd
