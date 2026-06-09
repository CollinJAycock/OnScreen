-- +goose Up
-- +goose StatementBegin
-- Add 'rtmp' to the allowed tuner-device types. An RTMP device is a live
-- broadcast ingest ("go live"): a broadcaster pushes to the embedded RTMP
-- server with the device's stream key and it surfaces as a Live TV channel.
-- Drop + re-add since the squashed 00001_init declares the constraint inline.
ALTER TABLE public.tuner_devices DROP CONSTRAINT IF EXISTS tuner_devices_type_check;
ALTER TABLE public.tuner_devices ADD CONSTRAINT tuner_devices_type_check
  CHECK (type = ANY (ARRAY['hdhomerun', 'm3u', 'rtmp']::text[]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.tuner_devices DROP CONSTRAINT IF EXISTS tuner_devices_type_check;
ALTER TABLE public.tuner_devices ADD CONSTRAINT tuner_devices_type_check
  CHECK (type = ANY (ARRAY['hdhomerun', 'm3u']::text[]));
-- +goose StatementEnd
