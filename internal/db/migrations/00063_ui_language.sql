-- +goose Up
-- +goose StatementBegin
-- The language a user chose for the apps' interface (v3 translations).
-- NULL = no choice: each app follows its device's language, falling back to
-- English. Stored on the account so the choice follows the user to every
-- device. The CHECK lists the languages the apps ship; adding one is a
-- migration so a client can't store a code no app understands.
ALTER TABLE users
    ADD COLUMN ui_language TEXT
        CONSTRAINT users_ui_language_check CHECK (ui_language IN ('en', 'es', 'zh-Hans'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN ui_language;
-- +goose StatementEnd
