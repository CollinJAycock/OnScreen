-- +goose Up
-- +goose StatementBegin
-- Podcast RSS subscriptions (internal/podcast). An admin subscribes a podcast
-- library to a feed; the feed's episodes are listed here and downloaded into
-- the library's folder, where the scanner turns them into the same
-- podcast / podcast_episode items a dropped-in file becomes. Clients play the
-- downloaded file, so nothing client-side changes.
--
-- podcast_feeds
--   folder        Absolute directory the episodes download into
--                 (<first scan path>/<feed title>). Fixed at subscribe time so
--                 a renamed feed keeps its folder.
--   auto_download New episodes found by a refresh download on their own.
--   keep_latest   How many downloaded episodes to keep, the newest by
--                 publish date; older downloads are deleted. 0 keeps all.
--   etag,         The feed's validators, sent back on the next refresh so an
--   last_modified unchanged feed answers 304.
--   show_item_id  The podcast item the scanner made for this feed, once an
--                 episode has been scanned.
--
-- podcast_feed_episodes
--   guid          The feed's <guid>, or the enclosure URL when it has none.
--   status        available   listed, not downloaded
--                 downloading claimed by a download in progress
--                 downloaded  file_path holds the file
--                 failed      the last download failed (error says why)
--                 removed     the file was deleted (retention or by hand);
--                             auto-download leaves it alone
--   file_path     Where the file goes. Set when a download is claimed, so the
--                 scanner can match the file to its episode the moment it
--                 lands.
CREATE TABLE public.podcast_feeds (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    library_id uuid NOT NULL REFERENCES public.libraries(id) ON DELETE CASCADE,
    feed_url text NOT NULL,
    title text NOT NULL,
    author text,
    description text,
    image_url text,
    site_url text,
    language text,
    folder text NOT NULL,
    auto_download boolean DEFAULT true NOT NULL,
    keep_latest integer DEFAULT 0 NOT NULL
        CONSTRAINT podcast_feeds_keep_latest_check CHECK (keep_latest >= 0),
    etag text,
    last_modified text,
    last_checked_at timestamp with time zone,
    last_error text,
    show_item_id uuid REFERENCES public.media_items(id) ON DELETE SET NULL,
    created_by uuid REFERENCES public.users(id) ON DELETE SET NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT podcast_feeds_library_url UNIQUE (library_id, feed_url)
);

CREATE INDEX podcast_feeds_library ON public.podcast_feeds (library_id);

CREATE TABLE public.podcast_feed_episodes (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    feed_id uuid NOT NULL REFERENCES public.podcast_feeds(id) ON DELETE CASCADE,
    guid text NOT NULL,
    title text NOT NULL,
    description text,
    published_at timestamp with time zone,
    duration_ms bigint,
    enclosure_url text NOT NULL,
    enclosure_type text,
    enclosure_length bigint,
    season integer,
    episode integer,
    image_url text,
    status text DEFAULT 'available' NOT NULL
        CONSTRAINT podcast_feed_episodes_status_check
        CHECK (status IN ('available', 'downloading', 'downloaded', 'failed', 'removed')),
    file_path text,
    error text,
    media_item_id uuid REFERENCES public.media_items(id) ON DELETE SET NULL,
    downloaded_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT podcast_feed_episodes_feed_guid UNIQUE (feed_id, guid)
);

CREATE INDEX podcast_feed_episodes_feed_published
    ON public.podcast_feed_episodes (feed_id, published_at DESC NULLS LAST);

CREATE INDEX podcast_feed_episodes_file_path
    ON public.podcast_feed_episodes (file_path)
    WHERE file_path IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.podcast_feed_episodes;
DROP TABLE IF EXISTS public.podcast_feeds;
-- +goose StatementEnd
