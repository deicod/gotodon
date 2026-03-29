-- Improve NodeInfo active user queries by indexing successful login activity by time and user.

-- +goose Up

CREATE INDEX IF NOT EXISTS index_login_activities_success_created_at_user_id
    ON login_activities (created_at DESC, user_id)
    WHERE success = true;

-- +goose Down

DROP INDEX IF EXISTS index_login_activities_success_created_at_user_id;
