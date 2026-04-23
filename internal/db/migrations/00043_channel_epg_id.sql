-- +goose Up
-- The XMLTV ingester needs a stable mapping from each OnScreen channel to
-- the channel ID an EPG source uses (XMLTV's <channel id="...">,
-- Schedules Direct's stationID). We store one such ID per channel — for
-- multi-source setups the operator picks which source's ID to use, since
-- different sources usually have overlapping coverage anyway.
--
-- Auto-population on ingest: when this is NULL, the ingester tries to
-- infer it by matching the source's <display-name>/lcn to channels.callsign
-- or channels.number. Manual override via the settings UI when auto-match
-- gets it wrong (or for IPTV channels that don't carry callsigns).

ALTER TABLE channels ADD COLUMN epg_channel_id TEXT;

CREATE INDEX idx_channels_epg_id ON channels(epg_channel_id)
    WHERE epg_channel_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_channels_epg_id;
ALTER TABLE channels DROP COLUMN epg_channel_id;
